use super::{ResolvedVendors, authority, client_hello};
use serde_json::{Value, json};
use std::collections::VecDeque;
use std::io::{self, Read, Write};
use std::net::{Shutdown, SocketAddr, TcpListener, TcpStream};
use std::os::fd::{AsRawFd, FromRawFd};
use std::time::{Duration, Instant};

const CONNECTION_LIMIT: usize = 16;
const BUFFER_LIMIT: usize = 64 * 1024;
const HEAD_LIMIT: usize = 16 * 1024;
const HANDSHAKE_TIMEOUT: Duration = Duration::from_secs(10);
const IDLE_TIMEOUT: Duration = Duration::from_secs(300);

type Recorder<'a> = dyn FnMut(Value) -> io::Result<()> + 'a;

#[derive(PartialEq)]
enum Phase {
    Head,
    Hello,
    Connecting,
    Tunnel,
}

struct Connection {
    id: u64,
    client: TcpStream,
    upstream: Option<TcpStream>,
    domain: String,
    phase: Phase,
    to_client: VecDeque<u8>,
    to_upstream: VecDeque<u8>,
    client_eof: bool,
    upstream_eof: bool,
    client_shutdown: bool,
    upstream_shutdown: bool,
    next_address: usize,
    started: Instant,
    progress: Instant,
    sent: u64,
    received: u64,
}

fn connecting(address: SocketAddr) -> io::Result<TcpStream> {
    let SocketAddr::V4(address) = address else {
        return Err(io::Error::other("IPv4 upstream required"));
    };
    // Socket creation and nonblocking connect are in the existing proxy
    // profile; no resolver, file opens or thread creation run here.
    let fd = unsafe { libc::socket(libc::AF_INET, libc::SOCK_STREAM, 0) };
    if fd < 0 {
        return Err(io::Error::last_os_error());
    }
    let stream = unsafe { TcpStream::from_raw_fd(fd) };
    if unsafe { libc::fcntl(fd, libc::F_SETFD, libc::FD_CLOEXEC) } < 0 {
        return Err(io::Error::last_os_error());
    }
    stream.set_nonblocking(true)?;
    let mut raw: libc::sockaddr_in = unsafe { std::mem::zeroed() };
    raw.sin_family = libc::AF_INET as _;
    raw.sin_port = address.port().to_be();
    raw.sin_addr.s_addr = u32::from_ne_bytes(address.ip().octets());
    #[cfg(target_os = "macos")]
    {
        raw.sin_len = std::mem::size_of_val(&raw) as _;
    }
    let result = unsafe {
        libc::connect(
            fd,
            (&raw const raw).cast(),
            std::mem::size_of_val(&raw) as _,
        )
    };
    if result < 0 {
        let error = io::Error::last_os_error();
        if error.raw_os_error() != Some(libc::EINPROGRESS) {
            return Err(error);
        }
    }
    Ok(stream)
}

fn read_into(
    stream: &mut TcpStream,
    buffer: &mut VecDeque<u8>,
    eof: &mut bool,
) -> io::Result<usize> {
    let mut bytes = [0; 8192];
    let size = bytes.len().min(BUFFER_LIMIT - buffer.len());
    if *eof || size == 0 {
        return Ok(0);
    }
    match stream.read(&mut bytes[..size]) {
        Ok(0) => {
            *eof = true;
            Ok(0)
        }
        Ok(size) => {
            buffer.extend(&bytes[..size]);
            Ok(size)
        }
        Err(error)
            if matches!(
                error.kind(),
                io::ErrorKind::WouldBlock | io::ErrorKind::Interrupted
            ) =>
        {
            Ok(0)
        }
        Err(error) => Err(error),
    }
}

fn write_from(stream: &mut TcpStream, buffer: &mut VecDeque<u8>) -> io::Result<usize> {
    if buffer.is_empty() {
        return Ok(0);
    }
    match stream.write(buffer.as_slices().0) {
        Ok(0) => Err(io::Error::from(io::ErrorKind::WriteZero)),
        Ok(size) => {
            buffer.drain(..size);
            Ok(size)
        }
        Err(error)
            if matches!(
                error.kind(),
                io::ErrorKind::WouldBlock | io::ErrorKind::Interrupted
            ) =>
        {
            Ok(0)
        }
        Err(error) => Err(error),
    }
}

impl Connection {
    fn new(id: u64, client: TcpStream) -> io::Result<Self> {
        client.set_nonblocking(true)?;
        Ok(Self {
            id,
            client,
            upstream: None,
            domain: String::new(),
            phase: Phase::Head,
            to_client: VecDeque::new(),
            to_upstream: VecDeque::new(),
            client_eof: false,
            upstream_eof: false,
            client_shutdown: false,
            upstream_shutdown: false,
            next_address: 0,
            started: Instant::now(),
            progress: Instant::now(),
            sent: 0,
            received: 0,
        })
    }

    fn dial(&mut self, vendors: &ResolvedVendors) -> io::Result<()> {
        for address in &vendors.addresses[&self.domain][self.next_address..] {
            self.next_address += 1;
            if let Ok(upstream) = connecting(*address) {
                self.upstream = Some(upstream);
                self.phase = Phase::Connecting;
                return Ok(());
            }
        }
        Err(io::Error::other("upstream_unavailable"))
    }

    fn handshake(
        &mut self,
        vendors: &ResolvedVendors,
        record: &mut Recorder<'_>,
    ) -> io::Result<()> {
        if self.phase == Phase::Head {
            let bytes = self.to_upstream.make_contiguous();
            if let Some(end) = bytes.windows(4).position(|part| part == b"\r\n\r\n") {
                if end + 4 > HEAD_LIMIT {
                    return Err(io::Error::other("invalid_head"));
                }
                let domains: Vec<_> = vendors.addresses.keys().cloned().collect();
                let head = std::str::from_utf8(&bytes[..end])
                    .map_err(|_| io::Error::other("invalid_head"))?;
                self.domain = authority(head, &domains)
                    .ok_or_else(|| io::Error::other("unapproved_authority"))?
                    .to_owned();
                self.to_upstream.drain(..end + 4);
                self.to_client
                    .extend(b"HTTP/1.1 200 Connection Established\r\n\r\n");
                self.phase = Phase::Hello;
            } else if bytes.len() >= HEAD_LIMIT {
                return Err(io::Error::other("invalid_head"));
            }
        }
        if self.phase == Phase::Hello {
            let bytes = self.to_upstream.make_contiguous();
            if bytes.len() < 5 {
                return Ok(());
            }
            let size = u16::from_be_bytes([bytes[3], bytes[4]]) as usize;
            if bytes[0] != 22
                || bytes[1] != 3
                || !(1..=3).contains(&bytes[2])
                || !(4..=16384).contains(&size)
            {
                return Err(io::Error::other("invalid_or_mismatched_client_hello"));
            }
            if bytes.len() < size + 5 {
                return Ok(());
            }
            if !client_hello(&bytes[5..5 + size], &self.domain) {
                return Err(io::Error::other("invalid_or_mismatched_client_hello"));
            }
            // Failure of this durable record propagates out of the entire
            // reactor, before either connect or payload forwarding.
            record(json!({"type":"tunnel_admitted","id":self.id,"vendor":self.domain,"port":443}))?;
            self.dial(vendors)?;
        }
        Ok(())
    }

    fn fds(&self) -> [libc::pollfd; 2] {
        let mut client = 0;
        if !self.client_eof && self.to_upstream.len() < BUFFER_LIMIT {
            client |= libc::POLLIN;
        }
        if !self.to_client.is_empty() {
            client |= libc::POLLOUT;
        }
        let mut upstream = 0;
        if self.phase == Phase::Connecting || !self.to_upstream.is_empty() {
            upstream |= libc::POLLOUT;
        }
        if !self.upstream_eof && self.to_client.len() < BUFFER_LIMIT {
            upstream |= libc::POLLIN;
        }
        [
            libc::pollfd {
                fd: if client == 0 {
                    -1
                } else {
                    self.client.as_raw_fd()
                },
                events: client,
                revents: 0,
            },
            libc::pollfd {
                fd: self
                    .upstream
                    .as_ref()
                    .filter(|_| upstream != 0)
                    .map_or(-1, AsRawFd::as_raw_fd),
                events: upstream,
                revents: 0,
            },
        ]
    }

    fn step(
        &mut self,
        events: [i16; 2],
        vendors: &ResolvedVendors,
        record: &mut Recorder<'_>,
    ) -> io::Result<bool> {
        if self.phase != Phase::Tunnel && self.started.elapsed() >= HANDSHAKE_TIMEOUT {
            return Err(io::Error::other("handshake_timeout"));
        }
        if self.progress.elapsed() >= IDLE_TIMEOUT {
            return Err(io::Error::other("idle_timeout"));
        }
        if events[0] & (libc::POLLERR | libc::POLLNVAL) != 0 {
            return Err(io::Error::other("client_error"));
        }
        let mut progress = 0;
        if events[0] & (libc::POLLIN | libc::POLLHUP) != 0 {
            progress += read_into(
                &mut self.client,
                &mut self.to_upstream,
                &mut self.client_eof,
            )?;
        }
        self.handshake(vendors, record)?;
        if self.client_eof && matches!(self.phase, Phase::Head | Phase::Hello) {
            return Err(io::Error::other("incomplete_handshake"));
        }
        if self.phase == Phase::Connecting
            && events[1] & (libc::POLLOUT | libc::POLLERR | libc::POLLHUP) != 0
        {
            if self.upstream.as_ref().unwrap().take_error()?.is_some() {
                self.upstream = None;
                self.dial(vendors)?;
                return Ok(false);
            }
            self.phase = Phase::Tunnel;
        }
        if self.phase == Phase::Tunnel {
            let upstream = self.upstream.as_mut().unwrap();
            if events[1] & (libc::POLLERR | libc::POLLNVAL) != 0 {
                return Err(io::Error::other("upstream_error"));
            }
            if events[1] & (libc::POLLIN | libc::POLLHUP) != 0 {
                let size = read_into(upstream, &mut self.to_client, &mut self.upstream_eof)?;
                self.received += size as u64;
                progress += size;
            }
            if events[1] & libc::POLLOUT != 0 {
                let size = write_from(upstream, &mut self.to_upstream)?;
                self.sent += size as u64;
                progress += size;
            }
            if self.client_eof && self.to_upstream.is_empty() && !self.upstream_shutdown {
                upstream.shutdown(Shutdown::Write)?;
                self.upstream_shutdown = true;
            }
            if self.upstream_eof && self.to_client.is_empty() && !self.client_shutdown {
                self.client.shutdown(Shutdown::Write)?;
                self.client_shutdown = true;
            }
        }
        if events[0] & libc::POLLOUT != 0 {
            progress += write_from(&mut self.client, &mut self.to_client)?;
        }
        if progress != 0 {
            self.progress = Instant::now();
        }
        Ok(self.client_eof
            && self.upstream_eof
            && self.to_client.is_empty()
            && self.to_upstream.is_empty())
    }
}

pub(super) struct Reactor<'a> {
    listener: TcpListener,
    vendors: &'a ResolvedVendors,
    connections: Vec<Connection>,
    next_id: u64,
}

impl<'a> Reactor<'a> {
    pub(super) fn new(listener: TcpListener, vendors: &'a ResolvedVendors) -> io::Result<Self> {
        listener.set_nonblocking(true)?;
        Ok(Self {
            listener,
            vendors,
            connections: Vec::new(),
            next_id: 0,
        })
    }

    pub(super) fn tick(&mut self, record: &mut Recorder<'_>) -> io::Result<()> {
        let mut fds = vec![libc::pollfd {
            fd: self.listener.as_raw_fd(),
            events: libc::POLLIN,
            revents: 0,
        }];
        for connection in &self.connections {
            fds.extend(connection.fds());
        }
        if unsafe { libc::poll(fds.as_mut_ptr(), fds.len() as _, 50) } < 0 {
            let error = io::Error::last_os_error();
            return if error.kind() == io::ErrorKind::Interrupted {
                Ok(())
            } else {
                Err(error)
            };
        }
        for index in (0..self.connections.len()).rev() {
            let connection = &mut self.connections[index];
            let mut audit_failed = false;
            let result = connection.step(
                [fds[1 + 2 * index].revents, fds[2 + 2 * index].revents],
                self.vendors,
                &mut |event| {
                    let result = record(event);
                    audit_failed |= result.is_err();
                    result
                },
            );
            if audit_failed {
                return result.map(|_| ());
            }
            match result {
                Ok(false) => continue,
                result => {
                    record(json!({
                        "type":if connection.phase == Phase::Tunnel { "tunnel_closed" } else { "tunnel_denied" },
                        "id":connection.id,"vendor":connection.domain,
                        "reason":result.err().map(|error| error.to_string()),
                        "sent_bytes":connection.sent,"received_bytes":connection.received,
                        "duration_ms":connection.started.elapsed().as_millis()
                    }))?;
                    self.connections.swap_remove(index);
                }
            }
        }
        // Bound accept work as well as the active set, so a connection flood
        // cannot starve already admitted tunnels or their timeout processing.
        if fds[0].revents & libc::POLLIN != 0 {
            for _ in 0..CONNECTION_LIMIT {
                let (client, _) = match self.listener.accept() {
                    Ok(value) => value,
                    Err(error) if error.kind() == io::ErrorKind::WouldBlock => break,
                    Err(error) if error.kind() == io::ErrorKind::Interrupted => continue,
                    Err(error) => return Err(error),
                };
                self.next_id = self
                    .next_id
                    .checked_add(1)
                    .ok_or_else(|| io::Error::other("connection ID exhausted"))?;
                if self.connections.len() >= CONNECTION_LIMIT {
                    record(
                        json!({"type":"tunnel_denied","id":self.next_id,"reason":"connection_limit"}),
                    )?;
                } else {
                    self.connections
                        .push(Connection::new(self.next_id, client)?);
                }
            }
        }
        Ok(())
    }
}
