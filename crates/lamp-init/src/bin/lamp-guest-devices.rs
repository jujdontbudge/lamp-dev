use lamp_init::{hex_decode, hex_encode};
use serde_json::{Value, json};
use std::collections::VecDeque;
use std::fs::{File, OpenOptions};
use std::io::{Read, Write};
use std::os::unix::ffi::OsStrExt;
use std::os::unix::fs::{DirBuilderExt, PermissionsExt};
use std::os::unix::net::{UnixListener, UnixStream};
use std::os::unix::process::CommandExt;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::sync::{Arc, Condvar, Mutex};
use std::time::{Duration, Instant};

type Queue = Arc<(Mutex<VecDeque<u8>>, Condvar)>;
const CAPACITY: usize = 4 << 20;

fn main() {
    if let Err(e) = run() {
        eprintln!("lamp-guest-devices: {e}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), Box<dyn std::error::Error>> {
    let a: Vec<String> = std::env::args().collect();
    if a.len() != 4 {
        return Err("usage: lamp-guest-devices start|serve|request DIRECTORY JSON".into());
    }
    let dir = Path::new(&a[2]);
    let data: Value = serde_json::from_str(&a[3])?;
    match a[1].as_str() {
        "record" => {
            let n = data
                .as_u64()
                .filter(|n| *n > 0 && *n <= 32768 && n % 2 == 0)
                .ok_or("record byte count must be positive, even and <=32768")?
                as usize;
            let mut bytes = vec![0u8; n];
            File::open(dir.join("input.pcm"))?.read_exact(&mut bytes)?;
            let mut f = OpenOptions::new()
                .write(true)
                .create_new(true)
                .open(dir.join("recording.wav"))?;
            f.write_all(b"RIFF")?;
            f.write_all(&(n as u32 + 36).to_le_bytes())?;
            f.write_all(b"WAVEfmt ")?;
            f.write_all(&16u32.to_le_bytes())?;
            f.write_all(&1u16.to_le_bytes())?;
            f.write_all(&1u16.to_le_bytes())?;
            f.write_all(&48000u32.to_le_bytes())?;
            f.write_all(&96000u32.to_le_bytes())?;
            f.write_all(&2u16.to_le_bytes())?;
            f.write_all(&16u16.to_le_bytes())?;
            f.write_all(b"data")?;
            f.write_all(&(n as u32).to_le_bytes())?;
            f.write_all(&bytes)?;
            f.sync_all()?;
            println!("{}", json!({"recorded_bytes":n}));
        }
        "play" => {
            let bytes = std::fs::read(dir.join("recording.wav"))?;
            if bytes.len() < 44 || &bytes[..4] != b"RIFF" || &bytes[36..40] != b"data" {
                return Err("invalid recorded WAV".into());
            }
            OpenOptions::new()
                .write(true)
                .open(dir.join("output.pcm"))?
                .write_all(&bytes[44..])?;
            println!("{}", json!({"played_bytes":bytes.len()-44}));
        }
        "start" => {
            let child = Command::new(std::env::current_exe()?)
                .args(["serve", &a[2], &a[3]])
                .stdin(Stdio::null())
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .process_group(0)
                .spawn()?;
            println!("{}", json!({"pid":child.id()}));
        }
        "serve" => serve(dir, data)?,
        "request" => {
            let mut s = UnixStream::connect(dir.join("control.sock"))?;
            s.set_read_timeout(Some(Duration::from_secs(15)))?;
            serde_json::to_writer(&mut s, &data)?;
            s.shutdown(std::net::Shutdown::Write)?;
            let response: Value = serde_json::from_reader(s)?;
            println!("{response}");
            if response.get("error").is_some() {
                return Err("device request refused".into());
            }
        }
        _ => return Err("unknown command".into()),
    }
    Ok(())
}

fn queue() -> Queue {
    Arc::new((Mutex::new(VecDeque::new()), Condvar::new()))
}

fn fifo(path: &Path) -> std::io::Result<()> {
    let p = std::ffi::CString::new(path.as_os_str().as_bytes())?;
    if unsafe { libc::mkfifo(p.as_ptr(), 0o600) } != 0 {
        return Err(std::io::Error::last_os_error());
    }
    Ok(())
}

fn input_audio(path: PathBuf, q: Queue) {
    std::thread::spawn(move || {
        loop {
            let Ok(mut out) = OpenOptions::new().write(true).open(&path) else {
                return;
            };
            loop {
                let mut bytes = q.0.lock().unwrap();
                while bytes.is_empty() {
                    bytes = q.1.wait(bytes).unwrap();
                }
                let chunk: Vec<u8> = bytes.iter().take(4096).copied().collect();
                drop(bytes);
                match out.write(&chunk) {
                    Ok(n) => {
                        q.0.lock().unwrap().drain(..n);
                        q.1.notify_all();
                    }
                    Err(_) => break,
                }
            }
        }
    });
}

fn output_audio(path: PathBuf, q: Queue) {
    std::thread::spawn(move || {
        loop {
            let Ok(mut input) = File::open(&path) else {
                return;
            };
            let mut buf = [0u8; 4096];
            while let Ok(n) = input.read(&mut buf) {
                if n == 0 {
                    break;
                }
                let mut bytes = q.0.lock().unwrap();
                while bytes.len() + n > CAPACITY {
                    bytes = q.1.wait(bytes).unwrap();
                }
                bytes.extend(&buf[..n]);
                q.1.notify_all();
            }
        }
    });
}

fn serve(dir: &Path, config: Value) -> Result<(), Box<dyn std::error::Error>> {
    std::fs::DirBuilder::new().mode(0o700).create(dir)?;
    let enabled = |s: &str| config[s].as_bool().unwrap_or(false);
    let (input, output) = (queue(), queue());
    if enabled("audio_input") {
        fifo(&dir.join("input.pcm"))?;
        input_audio(dir.join("input.pcm"), input.clone());
    }
    if enabled("audio_output") {
        fifo(&dir.join("output.pcm"))?;
        output_audio(dir.join("output.pcm"), output.clone());
    }
    let devices = Mutex::new(platform::Input::new(enabled("keyboard"), enabled("mouse"))?);
    let listener = UnixListener::bind(dir.join("control.sock"))?;
    std::fs::set_permissions(
        dir.join("control.sock"),
        std::fs::Permissions::from_mode(0o600),
    )?;
    for s in listener.incoming() {
        let mut s = s?;
        s.set_read_timeout(Some(Duration::from_secs(10)))?;
        let result: Result<Value, String> = (|| {
            let request: Value =
                serde_json::from_reader((&mut s).take(128 << 10)).map_err(|e| e.to_string())?;
            match request["op"].as_str().unwrap_or("") {
                "status" => Ok(
                    json!({"transport":"guest-vsock-command","format":"s16le","sample_rate":48000,"channels":1,"devices":config}),
                ),
                "recording" if enabled("audio_input") => {
                    let bytes =
                        std::fs::read(dir.join("recording.wav")).map_err(|e| e.to_string())?;
                    if bytes.len() > 32768 + 44 {
                        return Err("recording exceeds export limit".into());
                    }
                    Ok(json!({"wav_hex":hex_encode(&bytes)}))
                }
                "audio_input" if enabled("audio_input") => {
                    let bytes = hex_decode(request["pcm_hex"].as_str().ok_or("pcm_hex required")?)
                        .ok_or("invalid PCM hex")?;
                    if bytes.is_empty() || bytes.len() > 32768 || bytes.len() % 2 != 0 {
                        return Err("PCM chunk must contain 1..=16384 signed 16-bit samples".into());
                    }
                    let mut q = input.0.lock().unwrap();
                    if q.len() + bytes.len() > CAPACITY {
                        return Err("audio input backpressure".into());
                    }
                    q.extend(&bytes);
                    input.1.notify_all();
                    Ok(json!({"accepted_bytes":bytes.len()}))
                }
                "audio_output" if enabled("audio_output") => {
                    let n = request["bytes"].as_u64().ok_or("bytes required")? as usize;
                    if n == 0 || n > 32768 || !n.is_multiple_of(2) {
                        return Err("invalid output chunk size".into());
                    }
                    let deadline = Instant::now() + Duration::from_secs(10);
                    let mut q = output.0.lock().unwrap();
                    while q.len() < n {
                        let now = Instant::now();
                        if now >= deadline {
                            return Err("audio output not ready".into());
                        }
                        q = output.1.wait_timeout(q, deadline - now).unwrap().0;
                    }
                    let bytes: Vec<u8> = q.drain(..n).collect();
                    output.1.notify_all();
                    Ok(json!({"pcm_hex":hex_encode(&bytes),"read_bytes":n}))
                }
                "input" => {
                    let events = request["events"].as_array().ok_or("events required")?;
                    if events.is_empty() || events.len() > 32 {
                        return Err("1..=32 input events required".into());
                    }
                    for event in events {
                        match event["kind"].as_str() {
                            Some("key") if enabled("keyboard") => {}
                            Some("pointer") if enabled("mouse") => {}
                            _ => return Err("input device not requested".into()),
                        }
                    }
                    #[cfg(target_os = "macos")]
                    if let Some(endpoint) = config["input_endpoint"].as_str() {
                        for event in events {
                            if event["kind"] == "key" {
                                if event["code"].as_u64().filter(|v| *v < 128).is_none()
                                    || event["down"].as_bool().is_none()
                                {
                                    return Err("invalid macOS key event".into());
                                }
                            } else if ["x", "y"]
                                .iter()
                                .any(|k| event[k].as_u64().filter(|v| *v <= 65535).is_none())
                            {
                                return Err("invalid pointer coordinate".into());
                            }
                        }
                        let mut peer = UnixStream::connect(endpoint).map_err(|e| e.to_string())?;
                        peer.set_read_timeout(Some(Duration::from_secs(10)))
                            .map_err(|e| e.to_string())?;
                        peer.set_write_timeout(Some(Duration::from_secs(10)))
                            .map_err(|e| e.to_string())?;
                        serde_json::to_writer(&mut peer, &request).map_err(|e| e.to_string())?;
                        peer.shutdown(std::net::Shutdown::Write)
                            .map_err(|e| e.to_string())?;
                        let ack: Value =
                            serde_json::from_reader(peer.take(8192)).map_err(|e| e.to_string())?;
                        if ack["queued"].as_u64() != Some(events.len() as u64) {
                            return Err("guest application rejected input".into());
                        }
                        return Ok(
                            json!({"backend":"guest-AppKit-event-queue","queued_events":events.len()}),
                        );
                    }
                    devices.lock().unwrap().deliver(&request["events"])
                }
                #[cfg(target_os = "macos")]
                "input_state" if enabled("keyboard") || enabled("mouse") => {
                    platform::state(&request)
                }
                _ => Err("device not requested or unknown operation".into()),
            }
        })();
        let response = result.unwrap_or_else(|e| json!({"error":e}));
        let _ = serde_json::to_writer(&mut s, &response);
    }
    Ok(())
}

#[cfg(target_os = "linux")]
mod platform {
    use super::*;
    use std::os::fd::AsRawFd;
    #[repr(C)]
    struct Setup {
        name: [u8; 80],
        id: [u16; 4],
        ff: u32,
        max: [i32; 64],
        min: [i32; 64],
        fuzz: [i32; 64],
        flat: [i32; 64],
    }
    #[repr(C)]
    #[derive(Clone, Copy)]
    struct Event {
        time: libc::timeval,
        kind: u16,
        code: u16,
        value: i32,
    }
    pub struct Input {
        output: Option<File>,
        input: Option<File>,
    }
    impl Input {
        pub fn new(keyboard: bool, mouse: bool) -> std::io::Result<Self> {
            if !keyboard && !mouse {
                return Ok(Self {
                    output: None,
                    input: None,
                });
            }
            let mut f = OpenOptions::new().write(true).open("/dev/uinput")?;
            let ioctl = |request: libc::c_ulong, value: i32| -> std::io::Result<()> {
                if unsafe { libc::ioctl(f.as_raw_fd(), request as _, value) } < 0 {
                    Err(std::io::Error::last_os_error())
                } else {
                    Ok(())
                }
            };
            ioctl(0x40045564, 0)?;
            if keyboard {
                ioctl(0x40045564, 1)?;
                for code in 1..256 {
                    ioctl(0x40045565, code)?;
                }
            }
            if mouse {
                ioctl(0x40045564, 3)?;
                ioctl(0x40045567, 0)?;
                ioctl(0x40045567, 1)?;
            }
            let mut setup: Setup = unsafe { std::mem::zeroed() };
            setup.name[..18].copy_from_slice(b"Lamp virtual input");
            setup.id = [3, 0x1d6b, 0x104, 1];
            setup.max[0] = 65535;
            setup.max[1] = 65535;
            f.write_all(unsafe {
                std::slice::from_raw_parts(
                    (&setup as *const Setup).cast::<u8>(),
                    std::mem::size_of::<Setup>(),
                )
            })?;
            if unsafe { libc::ioctl(f.as_raw_fd(), 0x5501) } < 0 {
                return Err(std::io::Error::last_os_error());
            }
            let deadline = Instant::now() + Duration::from_secs(5);
            let event = loop {
                let found = std::fs::read_dir("/sys/class/input")?
                    .filter_map(Result::ok)
                    .map(|d| d.path())
                    .find(|p| {
                        p.file_name()
                            .unwrap()
                            .to_string_lossy()
                            .starts_with("event")
                            && std::fs::read_to_string(p.join("device/name"))
                                .unwrap_or_default()
                                .trim()
                                == "Lamp virtual input"
                    });
                if let Some(p) = found {
                    break OpenOptions::new()
                        .read(true)
                        .custom_flags(libc::O_NONBLOCK)
                        .open(Path::new("/dev/input").join(p.file_name().unwrap()))?;
                }
                if Instant::now() >= deadline {
                    return Err(std::io::Error::new(
                        std::io::ErrorKind::TimedOut,
                        "uinput device missing",
                    ));
                }
                std::thread::sleep(Duration::from_millis(10));
            };
            Ok(Self {
                output: Some(f),
                input: Some(event),
            })
        }
        pub fn deliver(&mut self, events: &Value) -> Result<Value, String> {
            let output = self.output.as_mut().ok_or("input not requested")?;
            let input = self.input.as_mut().unwrap();
            let mut sent = Vec::new();
            for e in events.as_array().ok_or("events required")? {
                if e["kind"] == "key" {
                    let code = e["code"]
                        .as_u64()
                        .filter(|v| (1..256).contains(v))
                        .ok_or("Linux keycode must be 1..255")?
                        as u16;
                    sent.push((
                        1,
                        code,
                        i32::from(e["down"].as_bool().ok_or("down required")?),
                    ));
                } else {
                    for (k, code) in [("x", 0), ("y", 1)] {
                        sent.push((
                            3,
                            code,
                            e[k].as_u64()
                                .filter(|v| *v <= 65535)
                                .ok_or("pointer coordinate must be 0..65535")?
                                as i32,
                        ));
                    }
                }
                sent.push((0, 0, 0));
            }
            for &(kind, code, value) in &sent {
                let e = Event {
                    time: unsafe { std::mem::zeroed() },
                    kind,
                    code,
                    value,
                };
                output
                    .write_all(unsafe {
                        std::slice::from_raw_parts(
                            (&e as *const Event).cast::<u8>(),
                            std::mem::size_of::<Event>(),
                        )
                    })
                    .map_err(|e| e.to_string())?;
            }
            let deadline = Instant::now() + Duration::from_secs(3);
            let mut observed = Vec::new();
            while observed.len() < sent.len() && Instant::now() < deadline {
                let mut bytes = [0u8; 24 * 32];
                match input.read(&mut bytes) {
                    Ok(n) => {
                        for b in bytes[..n].chunks_exact(std::mem::size_of::<Event>()) {
                            let e = unsafe { std::ptr::read_unaligned(b.as_ptr().cast::<Event>()) };
                            observed.push((e.kind, e.code, e.value));
                        }
                    }
                    Err(e) if e.kind() == std::io::ErrorKind::WouldBlock => {
                        std::thread::sleep(Duration::from_millis(5))
                    }
                    Err(e) => return Err(e.to_string()),
                }
            }
            if observed != sent {
                return Err(format!(
                    "evdev delivery mismatch: {observed:?} expected {sent:?}"
                ));
            }
            Ok(json!({"backend":"linux-uinput-evdev","observed_events":observed}))
        }
    }
    use std::os::unix::fs::OpenOptionsExt;
}

#[cfg(target_os = "macos")]
mod platform {
    use super::*;
    use std::ffi::c_void;
    #[repr(C)]
    #[derive(Clone, Copy)]
    struct Point {
        x: f64,
        y: f64,
    }
    type Ptr = *mut c_void;
    type Callback = unsafe extern "C" fn(Ptr, u32, Ptr, Ptr) -> Ptr;
    #[link(name = "ApplicationServices", kind = "framework")]
    unsafe extern "C" {
        fn CGEventCreateKeyboardEvent(source: Ptr, key: u16, down: bool) -> Ptr;
        fn CGEventCreateMouseEvent(source: Ptr, kind: u32, point: Point, button: u32) -> Ptr;
        fn CGEventSetIntegerValueField(event: Ptr, field: u32, value: i64);
        fn CGEventGetIntegerValueField(event: Ptr, field: u32) -> i64;
        fn CGEventGetLocation(event: Ptr) -> Point;
        fn CGEventPost(tap: u32, event: Ptr);
        fn CGEventCreate(source: Ptr) -> Ptr;
        fn CGEventSourceKeyState(state: i32, key: u16) -> bool;
        fn CGEventTapCreate(
            tap: u32,
            place: u32,
            options: u32,
            mask: u64,
            callback: Callback,
            user: Ptr,
        ) -> Ptr;
    }
    #[link(name = "CoreFoundation", kind = "framework")]
    unsafe extern "C" {
        fn CFMachPortCreateRunLoopSource(allocator: Ptr, port: Ptr, order: isize) -> Ptr;
        fn CFRunLoopGetCurrent() -> Ptr;
        fn CFRunLoopAddSource(runloop: Ptr, source: Ptr, mode: Ptr);
        fn CFRunLoopRemoveSource(runloop: Ptr, source: Ptr, mode: Ptr);
        fn CFMachPortInvalidate(port: Ptr);
        fn CFRunLoopRunInMode(mode: Ptr, seconds: f64, once: bool) -> i32;
        fn CFRelease(value: Ptr);
        static kCFRunLoopDefaultMode: Ptr;
    }
    struct Observations {
        tag: i64,
        events: Vec<Value>,
    }
    unsafe extern "C" fn observe(_: Ptr, kind: u32, event: Ptr, user: Ptr) -> Ptr {
        let state = unsafe { &mut *user.cast::<Observations>() };
        if unsafe { CGEventGetIntegerValueField(event, 42) } == state.tag {
            if kind == 10 || kind == 11 {
                state.events.push(json!({"kind":"key","code":unsafe{CGEventGetIntegerValueField(event,9)},"down":kind==10}));
            }
            if kind == 5 {
                let p = unsafe { CGEventGetLocation(event) };
                state.events.push(json!({"kind":"pointer","x":p.x,"y":p.y}));
            }
        }
        event
    }
    pub struct Input;
    pub fn state(request: &Value) -> Result<Value, String> {
        let key = request["code"]
            .as_u64()
            .filter(|v| *v < 128)
            .ok_or("keycode required")? as u16;
        unsafe {
            let event = CGEventCreate(std::ptr::null_mut());
            if event.is_null() {
                return Err("guest pointer state unavailable".into());
            }
            let point = CGEventGetLocation(event);
            CFRelease(event);
            Ok(
                json!({"code":key,"down":CGEventSourceKeyState(1,key),"session_down":CGEventSourceKeyState(0,key),"x":point.x,"y":point.y}),
            )
        }
    }
    struct Owned(Ptr);
    impl Drop for Owned {
        fn drop(&mut self) {
            unsafe { CFRelease(self.0) }
        }
    }
    impl Input {
        pub fn new(_: bool, _: bool) -> std::io::Result<Self> {
            Ok(Self)
        }
        pub fn deliver(&mut self, events: &Value) -> Result<Value, String> {
            let events = events.as_array().ok_or("events required")?;
            let mut made = Vec::new();
            for e in events {
                let event = unsafe {
                    if e["kind"] == "key" {
                        let code = e["code"]
                            .as_u64()
                            .filter(|v| *v < 128)
                            .ok_or("macOS keycode must be 0..127")?;
                        CGEventCreateKeyboardEvent(
                            std::ptr::null_mut(),
                            code as u16,
                            e["down"].as_bool().ok_or("down required")?,
                        )
                    } else {
                        let x = e["x"]
                            .as_f64()
                            .filter(|v| *v >= 0.0 && *v <= 65535.0)
                            .ok_or("invalid x")?;
                        let y = e["y"]
                            .as_f64()
                            .filter(|v| *v >= 0.0 && *v <= 65535.0)
                            .ok_or("invalid y")?;
                        CGEventCreateMouseEvent(std::ptr::null_mut(), 5, Point { x, y }, 0)
                    }
                };
                if event.is_null() {
                    return Err("guest event creation failed".into());
                }
                made.push(Owned(event));
            }
            let mut state = Box::new(Observations {
                tag: 0x4c414d500000 | unsafe { libc::getpid() } as i64,
                events: Vec::new(),
            });
            unsafe {
                let tap = CGEventTapCreate(
                    0,
                    1,
                    1,
                    (1 << 10) | (1 << 11) | (1 << 5),
                    observe,
                    (&mut *state as *mut Observations).cast(),
                );
                if tap.is_null() {
                    return Err("guest HID observation unavailable; configure an application input_endpoint".into());
                }
                let source = CFMachPortCreateRunLoopSource(std::ptr::null_mut(), tap, 0);
                if source.is_null() {
                    CFMachPortInvalidate(tap);
                    CFRelease(tap);
                    return Err("guest event source unavailable".into());
                }
                CFRunLoopAddSource(CFRunLoopGetCurrent(), source, kCFRunLoopDefaultMode);
                for event in &made {
                    CGEventSetIntegerValueField(event.0, 42, state.tag);
                    CGEventPost(0, event.0);
                }
                let deadline = Instant::now() + Duration::from_secs(3);
                while state.events.len() < events.len() && Instant::now() < deadline {
                    CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.05, false);
                }
                CFRunLoopRemoveSource(CFRunLoopGetCurrent(), source, kCFRunLoopDefaultMode);
                CFMachPortInvalidate(tap);
                CFRelease(source);
                CFRelease(tap);
            }
            if state.events.len() != events.len() {
                return Err(format!(
                    "guest event delivery incomplete: {:?}",
                    state.events
                ));
            }
            Ok(json!({"backend":"macos-quartz-hid","observed_events":state.events}))
        }
    }
}
