use super::*;
use crate::subscription_proxy::tests::hello;
use std::collections::BTreeMap;

const DOMAIN: &str = "api.anthropic.com";

fn request(domain: &str) -> Vec<u8> {
    let mut bytes =
        format!("CONNECT {DOMAIN}:443 HTTP/1.1\r\nHost: {DOMAIN}:443\r\n\r\n").into_bytes();
    let body = hello(domain);
    bytes.extend([22, 3, 1, (body.len() >> 8) as u8, body.len() as u8]);
    bytes.extend(body);
    bytes
}

fn vendors(origin: &TcpListener) -> ResolvedVendors {
    // Only transport tests inject loopback; the production constructor rejects
    // all private/host-denied answers and pins port 443.
    ResolvedVendors {
        addresses: [(DOMAIN.to_owned(), vec![origin.local_addr().unwrap()])].into(),
    }
}

fn receive(stream: &mut TcpStream, bytes: &mut Vec<u8>) {
    let mut buffer = [0; 8192];
    loop {
        match stream.read(&mut buffer) {
            Ok(0) => break,
            Ok(size) => bytes.extend_from_slice(&buffer[..size]),
            Err(error) if error.kind() == io::ErrorKind::WouldBlock => break,
            Err(error) => panic!("test peer read: {error}"),
        }
    }
}

fn exchange(confined: bool, fallback: bool) {
    let origin = TcpListener::bind("127.0.0.1:0").unwrap();
    origin.set_nonblocking(true).unwrap();
    let mut vendors = vendors(&origin);
    if fallback {
        let unavailable = TcpListener::bind("127.0.0.1:0").unwrap();
        vendors
            .addresses
            .get_mut(DOMAIN)
            .unwrap()
            .insert(0, unavailable.local_addr().unwrap());
        drop(unavailable);
    }
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let _stalled = TcpStream::connect(listener.local_addr().unwrap()).unwrap();
    let mut client = TcpStream::connect(listener.local_addr().unwrap()).unwrap();
    client.set_nonblocking(true).unwrap();
    let mut reactor = Reactor::new(listener, &vendors).unwrap();
    #[cfg(target_os = "linux")]
    if confined {
        crate::confinement::enter_with(
            65536,
            false,
            &[
                origin.as_raw_fd(),
                reactor.listener.as_raw_fd(),
                _stalled.as_raw_fd(),
                client.as_raw_fd(),
                1,
                2,
            ],
            crate::confinement::PROXY_SYSCALLS,
        )
        .unwrap();
    }
    let _ = confined;
    let mut wire = request(DOMAIN);
    let head = wire
        .windows(4)
        .position(|part| part == b"\r\n\r\n")
        .unwrap()
        + 4;
    wire.extend(b"request-private-canary");
    let expected = wire[head..].to_vec();
    let mut events = Vec::new();
    // Fragment both CONNECT and TLS, while another admitted socket stalls.
    for part in wire.chunks(3) {
        client.write_all(part).unwrap();
        reactor
            .tick(&mut |event| {
                events.push(event);
                Ok(())
            })
            .unwrap();
    }
    client.shutdown(Shutdown::Write).unwrap();
    let mut upstream = None;
    let mut sent_response = false;
    let mut received = Vec::new();
    let mut response = Vec::new();
    for _ in 0..100 {
        reactor
            .tick(&mut |event| {
                events.push(event);
                Ok(())
            })
            .unwrap();
        if upstream.is_none()
            && let Ok((stream, _)) = origin.accept()
        {
            stream.set_nonblocking(true).unwrap();
            upstream = Some(stream);
        }
        if let Some(stream) = &mut upstream {
            receive(stream, &mut received);
            if received == expected && !sent_response {
                stream.write_all(b"response-private-canary").unwrap();
                stream.shutdown(Shutdown::Write).unwrap();
                sent_response = true;
            }
        }
        receive(&mut client, &mut response);
        if reactor.connections.len() == 1 && response.ends_with(b"response-private-canary") {
            break;
        }
    }
    assert_eq!(received, expected);
    assert_eq!(
        response,
        b"HTTP/1.1 200 Connection Established\r\n\r\nresponse-private-canary"
    );
    assert_eq!(
        reactor.connections.len(),
        1,
        "stalled peer must not block a completed tunnel"
    );
    assert!(events.iter().any(|event| event["type"] == "tunnel_closed"));
    assert!(
        !serde_json::to_string(&events)
            .unwrap()
            .contains("private-canary")
    );
}

#[test]
fn fragmented_handshake_duplex_and_half_close_do_not_block_another_peer() {
    exchange(false, false);
}

#[test]
fn failed_connect_tries_only_the_next_pinned_address() {
    exchange(false, true);
}

#[test]
fn denied_authority_or_sni_never_dials_or_logs_request_data() {
    for bytes in [
        request("other.example"),
        b"CONNECT other.example:443 HTTP/1.1\r\nHost: other.example:443\r\n\r\n".to_vec(),
        format!("CONNECT {DOMAIN}:443 HTTP/1.1\r\nHost: {DOMAIN}:443\r\nX-Secret: private-canary\r\n\r\n").into_bytes(),
    ] {
        let origin = TcpListener::bind("127.0.0.1:0").unwrap();
        origin.set_nonblocking(true).unwrap();
        let vendors = vendors(&origin);
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let mut client = TcpStream::connect(listener.local_addr().unwrap()).unwrap();
        client.write_all(&bytes).unwrap();
        let mut reactor = Reactor::new(listener, &vendors).unwrap();
        let mut events = Vec::new();
        for _ in 0..3 {
            reactor.tick(&mut |event| { events.push(event); Ok(()) }).unwrap();
        }
        assert!(reactor.connections.is_empty());
        assert_eq!(origin.accept().unwrap_err().kind(), io::ErrorKind::WouldBlock);
        assert!(events.iter().any(|event| event["type"] == "tunnel_denied"));
        assert!(!serde_json::to_string(&events).unwrap().contains("private-canary"));
    }
}

#[test]
fn failed_admission_audit_stops_before_upstream_connect() {
    let origin = TcpListener::bind("127.0.0.1:0").unwrap();
    origin.set_nonblocking(true).unwrap();
    let vendors = vendors(&origin);
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let mut client = TcpStream::connect(listener.local_addr().unwrap()).unwrap();
    client.write_all(&request(DOMAIN)).unwrap();
    let mut reactor = Reactor::new(listener, &vendors).unwrap();
    reactor.tick(&mut |_| Ok(())).unwrap();
    assert!(
        reactor
            .tick(&mut |_| Err(io::Error::other("spool full")))
            .is_err()
    );
    assert_eq!(
        origin.accept().unwrap_err().kind(),
        io::ErrorKind::WouldBlock
    );
    assert!(reactor.connections[0].upstream.is_none());
}

#[test]
fn connection_capacity_and_absolute_handshake_deadline_are_bounded() {
    let vendors = ResolvedVendors {
        addresses: BTreeMap::new(),
    };
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let peers: Vec<_> = (0..CONNECTION_LIMIT + 1)
        .map(|_| TcpStream::connect(listener.local_addr().unwrap()).unwrap())
        .collect();
    let mut reactor = Reactor::new(listener, &vendors).unwrap();
    let mut events = Vec::new();
    for _ in 0..2 {
        reactor
            .tick(&mut |event| {
                events.push(event);
                Ok(())
            })
            .unwrap();
    }
    assert_eq!(reactor.connections.len(), CONNECTION_LIMIT);
    assert!(
        events
            .iter()
            .any(|event| event["reason"] == "connection_limit")
    );
    for connection in &mut reactor.connections {
        connection.started -= HANDSHAKE_TIMEOUT;
    }
    reactor
        .tick(&mut |event| {
            events.push(event);
            Ok(())
        })
        .unwrap();
    assert!(reactor.connections.is_empty());
    assert_eq!(
        events
            .iter()
            .filter(|event| event["reason"] == "handshake_timeout")
            .count(),
        CONNECTION_LIMIT
    );
    drop(peers);
}

#[test]
fn read_backpressure_and_wrapped_write_buffer_preserve_bytes() {
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let mut sender = TcpStream::connect(listener.local_addr().unwrap()).unwrap();
    let (mut receiver, _) = listener.accept().unwrap();
    receiver.set_nonblocking(true).unwrap();
    sender.write_all(b"abcdefgh").unwrap();
    let mut readable = libc::pollfd {
        fd: receiver.as_raw_fd(),
        events: libc::POLLIN,
        revents: 0,
    };
    assert_eq!(unsafe { libc::poll(&mut readable, 1, 1000) }, 1);
    let mut buffer = VecDeque::from(vec![b'x'; BUFFER_LIMIT - 3]);
    let mut eof = false;
    assert_eq!(read_into(&mut receiver, &mut buffer, &mut eof).unwrap(), 3);
    assert_eq!(buffer.len(), BUFFER_LIMIT);
    assert_eq!(read_into(&mut receiver, &mut buffer, &mut eof).unwrap(), 0);
    assert!(!eof);
    let mut pending = VecDeque::with_capacity(16);
    pending.extend(b"discard12345678");
    pending.drain(..7);
    pending.extend(b"abcdefgh");
    let expected: Vec<_> = pending.iter().copied().collect();
    while !pending.is_empty() {
        write_from(&mut receiver, &mut pending).unwrap();
    }
    let mut actual = vec![0; expected.len()];
    sender.read_exact(&mut actual).unwrap();
    assert_eq!(actual, expected);
}

#[test]
fn malformed_or_oversized_handshakes_are_bounded_and_never_dial() {
    let origin = TcpListener::bind("127.0.0.1:0").unwrap();
    origin.set_nonblocking(true).unwrap();
    let vendors = vendors(&origin);
    for bytes in [vec![b'x'; HEAD_LIMIT], {
        let mut bytes = request(DOMAIN);
        let end = bytes
            .windows(4)
            .position(|part| part == b"\r\n\r\n")
            .unwrap()
            + 4;
        bytes[end + 3] = 0xff;
        bytes
    }] {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let mut client = TcpStream::connect(listener.local_addr().unwrap()).unwrap();
        client.write_all(&bytes).unwrap();
        let mut reactor = Reactor::new(listener, &vendors).unwrap();
        for _ in 0..4 {
            reactor.tick(&mut |_| Ok(())).unwrap();
        }
        assert!(reactor.connections.is_empty());
        assert_eq!(
            origin.accept().unwrap_err().kind(),
            io::ErrorKind::WouldBlock
        );
    }
}

#[test]
fn would_block_write_retains_the_unsent_queue() {
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let _receiver = TcpStream::connect(listener.local_addr().unwrap()).unwrap();
    let (mut sender, _) = listener.accept().unwrap();
    sender.set_nonblocking(true).unwrap();
    let size: libc::c_int = 4096;
    assert_eq!(
        unsafe {
            libc::setsockopt(
                sender.as_raw_fd(),
                libc::SOL_SOCKET,
                libc::SO_SNDBUF,
                (&raw const size).cast(),
                std::mem::size_of_val(&size) as _,
            )
        },
        0
    );
    let mut pending = VecDeque::from(vec![b'x'; BUFFER_LIMIT]);
    let mut blocked = false;
    for _ in 0..1024 {
        if pending.is_empty() {
            pending.extend(vec![b'x'; BUFFER_LIMIT]);
        }
        let before = pending.clone();
        if write_from(&mut sender, &mut pending).unwrap() == 0 {
            assert_eq!(pending, before);
            blocked = true;
            break;
        }
    }
    assert!(
        blocked,
        "non-reading peer must eventually apply backpressure"
    );
}

#[cfg(target_os = "linux")]
#[test]
fn confined_event_loop_uses_the_unchanged_proxy_profile() {
    const CHILD: &str = "LAMP_SUBSCRIPTION_SECCOMP_CHILD";
    if std::env::var_os(CHILD).is_some() {
        exchange(true, false);
        std::process::exit(0);
    }
    if unsafe { libc::geteuid() } != 0 {
        eprintln!("root-only seccomp integration not exercised");
        return;
    }
    let status = std::process::Command::new(std::env::current_exe().unwrap())
        .args(["--exact", "subscription_proxy::tunnels::tests::confined_event_loop_uses_the_unchanged_proxy_profile", "--nocapture"])
        .env(CHILD, "1").status().unwrap();
    assert!(status.success(), "confined reactor child exited: {status}");
}
