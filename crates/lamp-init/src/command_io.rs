//! Guest-owned output spools. Unlinked descriptors cannot be replaced by task
//! paths, and each channel is read independently in bounded chunks.
use crate::{ExecIo, GuestResponse, b64_decode, b64_encode};
use std::fs::{File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::fd::{AsRawFd, FromRawFd};
use std::os::unix::fs::{FileExt, OpenOptionsExt};
use std::process::{Command, Stdio};
use std::sync::{
    Arc, Mutex,
    atomic::{AtomicBool, Ordering},
};

pub struct Spool {
    file: File,
    pub size: u64,
    pub failed: bool,
}
impl Spool {
    fn new() -> io::Result<Self> {
        let root = std::path::Path::new("/var/run/lamp-output");
        std::fs::create_dir_all(root)?;
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(root, std::fs::Permissions::from_mode(0o700))?;
        static NEXT: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);
        let path = root.join(format!(
            "{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::Relaxed)
        ));
        let file = OpenOptions::new()
            .create_new(true)
            .read(true)
            .write(true)
            .mode(0o600)
            .open(&path)?;
        std::fs::remove_file(path)?;
        Ok(Self {
            file,
            size: 0,
            failed: false,
        })
    }
    pub fn chunk(&self, offset: u64, length: usize) -> io::Result<Vec<u8>> {
        if offset > self.size {
            return Err(io::Error::other("output offset beyond end"));
        }
        let mut bytes = vec![0; length.min((self.size - offset) as usize)];
        self.file.read_exact_at(&mut bytes, offset)?;
        Ok(bytes)
    }
}
pub struct CommandIo {
    pub out: Arc<Mutex<Spool>>,
    pub err: Arc<Mutex<Spool>>,
    pub pid: i32,
    pub terminal: Option<File>,
    pub input: Option<Mutex<Option<std::process::ChildStdin>>>,
    pub finished: AtomicBool,
}
impl CommandIo {
    pub fn new() -> io::Result<Self> {
        Ok(Self {
            out: Arc::new(Mutex::new(Spool::new()?)),
            err: Arc::new(Mutex::new(Spool::new()?)),
            pid: 0,
            terminal: None,
            input: None,
            finished: AtomicBool::new(false),
        })
    }
    pub fn terminal(&mut self, command: &mut Command) -> io::Result<()> {
        let mut master = -1;
        let mut slave = -1;
        let mut size = libc::winsize {
            ws_row: 24,
            ws_col: 80,
            ws_xpixel: 0,
            ws_ypixel: 0,
        };
        if unsafe {
            libc::openpty(
                &mut master,
                &mut slave,
                std::ptr::null_mut(),
                std::ptr::null_mut(),
                &mut size,
            )
        } != 0
        {
            return Err(io::Error::last_os_error());
        }
        let master = unsafe { File::from_raw_fd(master) };
        let slave = unsafe { File::from_raw_fd(slave) };
        unsafe {
            libc::fcntl(master.as_raw_fd(), libc::F_SETFD, libc::FD_CLOEXEC);
            libc::fcntl(master.as_raw_fd(), libc::F_SETFL, libc::O_NONBLOCK);
            libc::fcntl(slave.as_raw_fd(), libc::F_SETFD, libc::FD_CLOEXEC);
        }
        command
            .stdin(Stdio::from(slave.try_clone()?))
            .stdout(Stdio::from(slave.try_clone()?))
            .stderr(Stdio::from(slave));
        self.terminal = Some(master);
        Ok(())
    }
    pub fn serve(&self, request: &ExecIo) -> io::Result<GuestResponse> {
        if request.signal != 0 {
            if ![2, 9, 15].contains(&request.signal) {
                return Err(io::Error::other("signal must be 2, 9 or 15"));
            }
            if !self.finished.load(Ordering::Acquire)
                && unsafe {
                    libc::killpg(
                        self.terminal
                            .as_ref()
                            .map(|t| libc::tcgetpgrp(t.as_raw_fd()))
                            .filter(|pid| *pid > 0)
                            .unwrap_or(self.pid),
                        request.signal,
                    )
                } != 0
            {
                return Err(io::Error::last_os_error());
            }
        }
        if !request.input_b64.is_empty() || request.input_eof {
            let bytes = b64_decode(&request.input_b64)
                .ok_or_else(|| io::Error::other("invalid input base64"))?;
            if bytes.len() > 4096 {
                return Err(io::Error::other("input exceeds 4096 bytes"));
            }
            if let Some(input) = &self.input {
                let mut input = input.lock().unwrap();
                if let Some(stream) = input.as_mut() {
                    if !bytes.is_empty() && write_input(stream, &bytes)? != bytes.len() {
                        return Err(io::Error::other("input partially delivered"));
                    }
                } else if !bytes.is_empty() {
                    return Err(io::Error::other("stdin already closed"));
                }
                if request.input_eof {
                    input.take();
                }
            } else {
                let mut terminal = self
                    .terminal
                    .as_ref()
                    .ok_or_else(|| io::Error::other("exec stdin is closed"))?;
                if write_input(&mut terminal, &bytes)? != bytes.len() {
                    return Err(io::Error::other("terminal input partially delivered"));
                }
            }
        }
        if request.rows > 0 || request.columns > 0 {
            let terminal = self
                .terminal
                .as_ref()
                .ok_or_else(|| io::Error::other("exec has no PTY"))?;
            let size = libc::winsize {
                ws_row: request.rows.max(1),
                ws_col: request.columns.max(1),
                ws_xpixel: 0,
                ws_ypixel: 0,
            };
            if unsafe { libc::ioctl(terminal.as_raw_fd(), libc::TIOCSWINSZ, &size) } < 0 {
                return Err(io::Error::last_os_error());
            }
        }
        let out = self.out.lock().unwrap();
        let err = self.err.lock().unwrap();
        let stdout = out.chunk(request.stdout_offset, 24576)?;
        let stderr = err.chunk(request.stderr_offset, 24576)?;
        Ok(GuestResponse::SessionReply { response_json: serde_json::json!({"stdout_base64": b64_encode(&stdout), "stderr_base64": b64_encode(&stderr), "stdout_bytes":out.size,"stderr_bytes":err.size,"truncated":out.failed || err.failed}).to_string() })
    }
}
pub fn capture(
    mut source: impl Read + Send + 'static,
    spool: Arc<Mutex<Spool>>,
    pid: i32,
) -> std::sync::mpsc::Receiver<()> {
    let (tx, rx) = std::sync::mpsc::channel();
    std::thread::spawn(move || {
        let mut bytes = [0u8; 32768];
        loop {
            match source.read(&mut bytes) {
                Ok(0) => break,
                Err(e) if e.raw_os_error() == Some(libc::EIO) => break, // PTY EOF
                Err(e) if e.kind() == io::ErrorKind::WouldBlock => {
                    std::thread::sleep(std::time::Duration::from_millis(5));
                    continue;
                }
                Err(_) => {
                    spool.lock().unwrap().failed = true;
                    break;
                }
                Ok(n) => {
                    let mut s = spool.lock().unwrap();
                    let offset = s.size;
                    if s.file.write_all_at(&bytes[..n], offset).is_err() {
                        s.failed = true;
                        unsafe {
                            libc::killpg(pid, libc::SIGKILL);
                        }
                        break;
                    }
                    s.size += n as u64;
                }
            }
        }
        let _ = tx.send(());
    });
    rx
}

// A stopped reader must not block the guest control channel. Pipe chunks are
// at most PIPE_BUF; a full pipe therefore accepts none and can be retried.
fn write_input(stream: &mut impl Write, bytes: &[u8]) -> io::Result<usize> {
    let until = std::time::Instant::now() + std::time::Duration::from_millis(500);
    loop {
        match stream.write(bytes) {
            Err(e) if e.kind() == io::ErrorKind::Interrupted => continue,
            Err(e)
                if e.kind() == io::ErrorKind::WouldBlock && std::time::Instant::now() < until =>
            {
                std::thread::sleep(std::time::Duration::from_millis(5));
            }
            result => return result,
        }
    }
}
