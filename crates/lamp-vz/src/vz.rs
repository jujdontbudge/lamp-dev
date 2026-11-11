//! The thin Virtualization.framework boundary.
//!
//! Every call goes through the generated `objc2-virtualization` bindings; this
//! module only turns them into blocking calls on the machine's own serial
//! dispatch queue. It holds no control state: the worker decides what to do.
use block2::RcBlock;
use dispatch2::{DispatchQueue, DispatchRetained};
use objc2::rc::Retained;
use objc2::runtime::AnyObject;
use objc2::{AnyThread, Message};
use objc2_foundation::{NSArray, NSData, NSError, NSString, NSURL};
use objc2_virtualization::*;
use std::os::fd::{FromRawFd, OwnedFd};
use std::path::{Path, PathBuf};
use std::sync::mpsc;
use std::time::Duration;

/// Moves a value that is only ever touched on the machine queue (or is
/// reference-count-safe) across a thread boundary.
pub(crate) struct OnQueue<T>(pub T);
// SAFETY: wrapped values are used only on the VM's serial queue, or are
// Objective-C objects whose retain/release is thread-safe.
unsafe impl<T> Send for OnQueue<T> {}

pub type VzResult<T> = std::result::Result<T, String>;

fn ns_url(path: &Path) -> Retained<NSURL> {
    NSURL::fileURLWithPath(&NSString::from_str(&path.to_string_lossy()))
}

pub(crate) fn describe(e: &NSError) -> String {
    let code = e.code();
    let domain = e.domain().to_string();
    let text = e.localizedDescription().to_string();
    let reason = e
        .localizedFailureReason()
        .map(|r| format!(" ({r})"))
        .unwrap_or_default();
    format!("{domain} {code}: {text}{reason}")
}

fn err_of(e: *mut NSError) -> Option<String> {
    // SAFETY: the framework passes a valid NSError or nil.
    unsafe { e.as_ref() }.map(describe)
}

/// One disk: a raw image file attached as a virtio block device.
#[derive(Debug, Clone)]
pub struct Disk {
    pub path: PathBuf,
    pub read_only: bool,
    /// `full` makes guest flushes reach stable storage (F_FULLFSYNC);
    /// `fsync` is an ordinary fsync; `none` never syncs (volatile scratch).
    pub sync: DiskSync,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DiskSync {
    Full,
    Fsync,
    None,
}

/// A Linux guest booted directly from a kernel image.
#[derive(Debug, Clone)]
pub struct LinuxSpec {
    pub kernel: PathBuf,
    pub initrd: Option<PathBuf>,
    pub cmdline: String,
    pub cpus: usize,
    pub memory_bytes: u64,
    pub disks: Vec<Disk>,
    /// Guest console output (hvc0) is appended here.
    pub console_log: Option<PathBuf>,
    /// `VZGenericMachineIdentifier` bytes. A restore must present the same
    /// hardware identity the state was saved with; the guest-visible Lamp
    /// identity is reset afterwards by the identity barrier.
    pub machine_identifier: Option<Vec<u8>>,
    /// A data NIC is attached only when networking is requested.
    pub nat_network: bool,
    pub devices: crate::spec::Devices,
}

/// A native macOS guest.
#[derive(Debug, Clone)]
pub struct MacSpec {
    pub hardware_model: Vec<u8>,
    pub machine_identifier: Vec<u8>,
    pub auxiliary_storage: PathBuf,
    pub cpus: usize,
    pub memory_bytes: u64,
    pub disks: Vec<Disk>,
    pub console_log: Option<PathBuf>,
    pub nat_network: bool,
    /// Headless display size; the framework still models a graphics device.
    pub display: (isize, isize),
    pub devices: crate::spec::Devices,
}

fn disk_attachment(d: &Disk) -> VzResult<Retained<VZStorageDeviceConfiguration>> {
    let mode = match d.sync {
        DiskSync::Full => VZDiskImageSynchronizationMode::Full,
        DiskSync::Fsync => VZDiskImageSynchronizationMode::Fsync,
        DiskSync::None => VZDiskImageSynchronizationMode::None,
    };
    // SAFETY: plain initialisers with valid arguments.
    unsafe {
        let att = VZDiskImageStorageDeviceAttachment::initWithURL_readOnly_cachingMode_synchronizationMode_error(
            VZDiskImageStorageDeviceAttachment::alloc(),
            &ns_url(&d.path),
            d.read_only,
            VZDiskImageCachingMode::Automatic,
            mode,
        )
        .map_err(|e| format!("disk {}: {}", d.path.display(), describe(&e)))?;
        let blk = VZVirtioBlockDeviceConfiguration::initWithAttachment(
            VZVirtioBlockDeviceConfiguration::alloc(),
            &att,
        );
        Ok(Retained::into_super(blk))
    }
}

fn console(path: &Path) -> VzResult<Retained<VZSerialPortConfiguration>> {
    // SAFETY: plain initialisers with valid arguments.
    unsafe {
        let att = VZFileSerialPortAttachment::initWithURL_append_error(
            VZFileSerialPortAttachment::alloc(),
            &ns_url(path),
            true,
        )
        .map_err(|e| format!("console {}: {}", path.display(), describe(&e)))?;
        let port = VZVirtioConsoleDeviceSerialPortConfiguration::new();
        port.setAttachment(Some(&att));
        Ok(Retained::into_super(port))
    }
}

fn common(
    cfg: &VZVirtualMachineConfiguration,
    cpus: usize,
    memory: u64,
    disks: &[Disk],
    log: Option<&Path>,
    nat: bool,
) -> VzResult<()> {
    // SAFETY: setters on a configuration this thread owns.
    unsafe {
        cfg.setCPUCount(cpus);
        cfg.setMemorySize(memory);
        let disks: Vec<_> = disks.iter().map(disk_attachment).collect::<VzResult<_>>()?;
        cfg.setStorageDevices(&NSArray::from_retained_slice(&disks));
        let vsock = Retained::into_super(VZVirtioSocketDeviceConfiguration::new());
        cfg.setSocketDevices(&NSArray::from_retained_slice(&[vsock]));
        let entropy = Retained::into_super(VZVirtioEntropyDeviceConfiguration::new());
        cfg.setEntropyDevices(&NSArray::from_retained_slice(&[entropy]));
        if let Some(log) = log {
            cfg.setSerialPorts(&NSArray::from_retained_slice(&[console(log)?]));
        }
        if nat {
            let net = VZVirtioNetworkDeviceConfiguration::new();
            let att = VZNATNetworkDeviceAttachment::new();
            net.setAttachment(Some(&att));
            cfg.setNetworkDevices(&NSArray::from_retained_slice(&[Retained::into_super(net)]));
        }
    }
    Ok(())
}

fn validated(
    cfg: Retained<VZVirtualMachineConfiguration>,
) -> VzResult<Retained<VZVirtualMachineConfiguration>> {
    // SAFETY: validation reads the configuration only.
    unsafe {
        cfg.validateWithError()
            .map_err(|e| format!("invalid configuration: {}", describe(&e)))?;
        cfg.validateSaveRestoreSupportWithError()
            .map_err(|e| format!("save/restore unsupported: {}", describe(&e)))?;
    }
    Ok(cfg)
}

pub fn linux_config(spec: &LinuxSpec) -> VzResult<Retained<VZVirtualMachineConfiguration>> {
    spec.devices.validate(false)?;
    // SAFETY: building a fresh configuration on this thread.
    unsafe {
        let boot =
            VZLinuxBootLoader::initWithKernelURL(VZLinuxBootLoader::alloc(), &ns_url(&spec.kernel));
        boot.setCommandLine(&NSString::from_str(&spec.cmdline));
        if let Some(i) = &spec.initrd {
            boot.setInitialRamdiskURL(Some(&ns_url(i)));
        }
        let platform = VZGenericPlatformConfiguration::new();
        if let Some(id) = &spec.machine_identifier {
            let data = NSData::with_bytes(id);
            let mid = VZGenericMachineIdentifier::initWithDataRepresentation(
                VZGenericMachineIdentifier::alloc(),
                &data,
            )
            .ok_or("invalid generic machine identifier")?;
            platform.setMachineIdentifier(&mid);
        }
        let cfg = VZVirtualMachineConfiguration::new();
        cfg.setBootLoader(Some(&boot));
        cfg.setPlatform(&platform);
        common(
            &cfg,
            spec.cpus,
            spec.memory_bytes,
            &spec.disks,
            spec.console_log.as_deref(),
            spec.nat_network,
        )?;
        if let Some(d) = &spec.devices.display {
            let gfx = VZVirtioGraphicsDeviceConfiguration::new();
            let scanout =
                VZVirtioGraphicsScanoutConfiguration::initWithWidthInPixels_heightInPixels(
                    VZVirtioGraphicsScanoutConfiguration::alloc(),
                    d.width as isize,
                    d.height as isize,
                );
            gfx.setScanouts(&NSArray::from_retained_slice(&[scanout]));
            cfg.setGraphicsDevices(&NSArray::from_retained_slice(&[Retained::into_super(gfx)]));
        }
        if spec.devices.keyboard {
            cfg.setKeyboards(&NSArray::from_retained_slice(&[Retained::into_super(
                VZUSBKeyboardConfiguration::new(),
            )]));
        }
        if spec.devices.mouse {
            cfg.setPointingDevices(&NSArray::from_retained_slice(&[Retained::into_super(
                VZUSBScreenCoordinatePointingDeviceConfiguration::new(),
            )]));
        }
        if spec.devices != crate::spec::Devices::default() {
            cfg.validateWithError().map_err(|e| describe(&e))?;
            return Ok(cfg);
        }
        validated(cfg)
    }
}

pub fn mac_config(spec: &MacSpec) -> VzResult<Retained<VZVirtualMachineConfiguration>> {
    spec.devices.validate(true)?;
    // SAFETY: building a fresh configuration on this thread.
    unsafe {
        let platform = VZMacPlatformConfiguration::new();
        let hw = VZMacHardwareModel::initWithDataRepresentation(
            VZMacHardwareModel::alloc(),
            &NSData::with_bytes(&spec.hardware_model),
        )
        .ok_or("invalid hardware model")?;
        if !hw.isSupported() {
            return Err("hardware model is not supported on this host".into());
        }
        let mid = VZMacMachineIdentifier::initWithDataRepresentation(
            VZMacMachineIdentifier::alloc(),
            &NSData::with_bytes(&spec.machine_identifier),
        )
        .ok_or("invalid machine identifier")?;
        let aux = VZMacAuxiliaryStorage::initWithURL(
            VZMacAuxiliaryStorage::alloc(),
            &ns_url(&spec.auxiliary_storage),
        );
        platform.setHardwareModel(&hw);
        platform.setMachineIdentifier(&mid);
        platform.setAuxiliaryStorage(Some(&aux));
        let cfg = VZVirtualMachineConfiguration::new();
        cfg.setBootLoader(Some(&VZMacOSBootLoader::new()));
        cfg.setPlatform(&platform);
        if let Some(d) = &spec.devices.display {
            let gfx = VZMacGraphicsDeviceConfiguration::new();
            let display =
            VZMacGraphicsDisplayConfiguration::initWithWidthInPixels_heightInPixels_pixelsPerInch(
                VZMacGraphicsDisplayConfiguration::alloc(),
                d.width as isize,
                d.height as isize,
                80,
            );
            gfx.setDisplays(&NSArray::from_retained_slice(&[display]));
            cfg.setGraphicsDevices(&NSArray::from_retained_slice(&[Retained::into_super(gfx)]));
        }
        if spec.devices.keyboard {
            let kb = Retained::into_super(VZMacKeyboardConfiguration::new());
            cfg.setKeyboards(&NSArray::from_retained_slice(&[kb]));
        }
        if spec.devices.mouse {
            let tp = Retained::into_super(VZMacTrackpadConfiguration::new());
            cfg.setPointingDevices(&NSArray::from_retained_slice(&[tp]));
        }
        common(
            &cfg,
            spec.cpus,
            spec.memory_bytes,
            &spec.disks,
            spec.console_log.as_deref(),
            spec.nat_network,
        )?;
        if spec.devices != crate::spec::Devices::default() {
            cfg.validateWithError().map_err(|e| describe(&e))?;
            return Ok(cfg);
        }
        validated(cfg)
    }
}

/// A new random `VZGenericMachineIdentifier`.
pub fn new_generic_machine_identifier() -> Vec<u8> {
    // SAFETY: plain constructor.
    unsafe {
        VZGenericMachineIdentifier::new()
            .dataRepresentation()
            .to_vec()
    }
}

/// A new random `VZMacMachineIdentifier` (the supported way to give a Mac
/// guest its own hardware identity).
pub fn new_mac_machine_identifier() -> Vec<u8> {
    // SAFETY: plain constructor.
    unsafe { VZMacMachineIdentifier::new().dataRepresentation().to_vec() }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum State {
    Stopped,
    Running,
    Paused,
    Error,
    Starting,
    Pausing,
    Resuming,
    Stopping,
    Saving,
    Restoring,
    Unknown(isize),
}

impl State {
    pub fn name(self) -> &'static str {
        match self {
            State::Stopped => "stopped",
            State::Running => "running",
            State::Paused => "paused",
            State::Error => "error",
            State::Starting => "starting",
            State::Pausing => "pausing",
            State::Resuming => "resuming",
            State::Stopping => "stopping",
            State::Saving => "saving",
            State::Restoring => "restoring",
            State::Unknown(_) => "unknown",
        }
    }
}

/// A live vsock connection to the guest. The framework closes the socket
/// when the connection object is released, so it is kept with the fd.
pub struct GuestConn {
    pub fd: OwnedFd,
    _conn: OnQueue<Retained<VZVirtioSocketConnection>>,
}

/// One virtual machine and its private serial queue.
pub struct Vm {
    vm: OnQueue<Retained<VZVirtualMachine>>,
    queue: DispatchRetained<DispatchQueue>,
}

// SAFETY: the machine object is touched only on `queue`.
unsafe impl Send for Vm {}
unsafe impl Sync for Vm {}

const OP_TIMEOUT: Duration = Duration::from_secs(600);

impl Vm {
    pub fn new(label: &str, cfg: Retained<VZVirtualMachineConfiguration>) -> Vm {
        let queue = DispatchQueue::new(label, None);
        let (tx, rx) = mpsc::channel();
        let cfg = OnQueue(cfg);
        let q2 = OnQueue(queue.clone());
        queue.exec_sync(move || {
            let cfg = cfg;
            let q2 = q2;
            // SAFETY: created on its own queue, as the framework requires.
            let vm = unsafe {
                VZVirtualMachine::initWithConfiguration_queue(
                    VZVirtualMachine::alloc(),
                    &cfg.0,
                    &q2.0,
                )
            };
            let _ = tx.send(OnQueue(vm));
        });
        Vm {
            vm: rx.recv().expect("vm created"),
            queue,
        }
    }

    fn on_queue<R: Send + 'static>(
        &self,
        f: impl FnOnce(&VZVirtualMachine) -> R + Send + 'static,
    ) -> R {
        let (tx, rx) = mpsc::channel();
        let vm = OnQueue(self.vm.0.retain());
        self.queue.exec_sync(move || {
            let vm = vm;
            let _ = tx.send(f(&vm.0));
        });
        rx.recv().expect("queue call")
    }

    /// Issues an asynchronous framework call on the queue and waits for its
    /// completion handler.
    fn completion(
        &self,
        what: &str,
        f: impl FnOnce(&VZVirtualMachine, &block2::DynBlock<dyn Fn(*mut NSError)>) + Send + 'static,
    ) -> VzResult<()> {
        let (tx, rx) = mpsc::channel::<Option<String>>();
        let block = OnQueue(RcBlock::new(move |e: *mut NSError| {
            let _ = tx.send(err_of(e));
        }));
        let vm = OnQueue(self.vm.0.retain());
        self.queue.exec_async(move || {
            let (vm, block) = (vm, block);
            f(&vm.0, &block.0);
        });
        match rx.recv_timeout(OP_TIMEOUT) {
            Ok(None) => Ok(()),
            Ok(Some(e)) => Err(format!("{what}: {e}")),
            Err(_) => Err(format!("{what}: no completion within {OP_TIMEOUT:?}")),
        }
    }

    pub fn state(&self) -> State {
        // SAFETY: state read on the queue.
        let s = self.on_queue(|vm| unsafe { vm.state() }.0);
        match s {
            0 => State::Stopped,
            1 => State::Running,
            2 => State::Paused,
            3 => State::Error,
            4 => State::Starting,
            5 => State::Pausing,
            6 => State::Resuming,
            7 => State::Stopping,
            8 => State::Saving,
            9 => State::Restoring,
            n => State::Unknown(n),
        }
    }

    pub fn start(&self) -> VzResult<()> {
        // SAFETY: framework call on its queue with a live completion block.
        self.completion("start", |vm, b| unsafe { vm.startWithCompletionHandler(b) })
    }

    pub fn pause(&self) -> VzResult<()> {
        // SAFETY: as above.
        self.completion("pause", |vm, b| unsafe { vm.pauseWithCompletionHandler(b) })
    }

    pub fn resume(&self) -> VzResult<()> {
        // SAFETY: as above.
        self.completion("resume", |vm, b| unsafe {
            vm.resumeWithCompletionHandler(b)
        })
    }

    /// Hard stop (no guest shutdown).
    pub fn stop(&self) -> VzResult<()> {
        // SAFETY: as above.
        self.completion("stop", |vm, b| unsafe { vm.stopWithCompletionHandler(b) })
    }

    /// Asks the guest to power off (ACPI-style request).
    pub fn request_stop(&self) -> VzResult<()> {
        // SAFETY: framework call on its queue.
        self.on_queue(|vm| unsafe { vm.requestStopWithError() }.map_err(|e| describe(&e)))
    }

    /// Saves the paused machine's full state (all guest RAM and devices).
    pub fn save(&self, path: &Path) -> VzResult<()> {
        let url = OnQueue(ns_url(path));
        // SAFETY: as above.
        self.completion("save", move |vm, b| {
            let url = url;
            unsafe { vm.saveMachineStateToURL_completionHandler(&url.0, b) }
        })
    }

    /// Restores a saved state into this (never started) machine; it is left
    /// paused.
    pub fn restore(&self, path: &Path) -> VzResult<()> {
        let url = OnQueue(ns_url(path));
        // SAFETY: as above.
        self.completion("restore", move |vm, b| {
            let url = url;
            unsafe { vm.restoreMachineStateFromURL_completionHandler(&url.0, b) }
        })
    }

    /// Connects to a guest vsock port.
    pub fn connect(&self, port: u32) -> VzResult<GuestConn> {
        let (tx, rx) = mpsc::channel::<VzResult<OnQueue<Retained<VZVirtioSocketConnection>>>>();
        let vm = OnQueue(self.vm.0.retain());
        self.queue.exec_async(move || {
            let vm = vm;
            // SAFETY: the device list and connect call run on the queue.
            unsafe {
                let devices = vm.0.socketDevices();
                let Some(dev) = devices.firstObject() else {
                    let _ = tx.send(Err("no socket device".into()));
                    return;
                };
                let Ok(dev) = Retained::downcast::<VZVirtioSocketDevice>(dev) else {
                    let _ = tx.send(Err("socket device is not virtio".into()));
                    return;
                };
                let block =
                    RcBlock::new(move |c: *mut VZVirtioSocketConnection, e: *mut NSError| {
                        let r = match (Retained::retain(c), err_of(e)) {
                            (_, Some(e)) => Err(e),
                            (Some(c), None) => Ok(OnQueue(c)),
                            (None, None) => Err("no connection".into()),
                        };
                        let _ = tx.send(r);
                    });
                dev.connectToPort_completionHandler(port, &block);
            }
        });
        let conn = rx
            .recv_timeout(Duration::from_secs(30))
            .map_err(|_| "vsock connect: no completion".to_string())??;
        // SAFETY: the connection's descriptor stays open while `conn` lives;
        // dup gives this side its own reference.
        let fd = unsafe { libc::dup(conn.0.fileDescriptor()) };
        if fd < 0 {
            return Err(format!("dup: {}", std::io::Error::last_os_error()));
        }
        Ok(GuestConn {
            // SAFETY: fresh descriptor from dup.
            fd: unsafe { OwnedFd::from_raw_fd(fd) },
            _conn: conn,
        })
    }
}

impl Drop for Vm {
    fn drop(&mut self) {
        // Release the machine on its own queue.
        let vm = OnQueue(self.vm.0.retain());
        self.queue.exec_sync(move || drop(vm));
    }
}

/// Whether the framework is usable by this process (entitlement, host).
pub fn supported() -> bool {
    // SAFETY: class property read.
    unsafe { VZVirtualMachine::isSupported() }
}

pub fn host_limits() -> serde_json::Value {
    // SAFETY: class property reads.
    unsafe {
        serde_json::json!({
            "min_memory": VZVirtualMachineConfiguration::minimumAllowedMemorySize(),
            "max_memory": VZVirtualMachineConfiguration::maximumAllowedMemorySize(),
            "min_cpus": VZVirtualMachineConfiguration::minimumAllowedCPUCount(),
            "max_cpus": VZVirtualMachineConfiguration::maximumAllowedCPUCount(),
        })
    }
}

#[allow(dead_code)]
fn _assert_object(_: &AnyObject) {}
