//! Native macOS media and installation through the supported framework APIs.
//!
//! Lamp never redistributes macOS. The restore image is fetched by the
//! owner's own Mac from Apple (`fetchLatestSupported`) or read from a file
//! the owner supplies, and the guest is installed with `VZMacOSInstaller`.
use crate::vz::{OnQueue, VzResult, describe};
use block2::RcBlock;
use dispatch2::DispatchQueue;
use objc2::rc::Retained;
use objc2::{AnyThread, Message};
use objc2_foundation::{NSError, NSString, NSURL};
use objc2_virtualization::*;
use std::path::Path;
use std::sync::mpsc;
use std::time::Duration;

/// What a restore image declares.
#[derive(Debug, Clone, serde::Serialize)]
pub struct RestoreImage {
    pub url: String,
    pub build: String,
    pub version: String,
    pub supported: bool,
    pub hardware_model_b64: Option<String>,
    pub min_cpus: Option<usize>,
    pub min_memory: Option<u64>,
}

pub fn b64_encode(bytes: &[u8]) -> String {
    const T: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    let mut out = String::new();
    for c in bytes.chunks(3) {
        let n = (c[0] as u32) << 16
            | (*c.get(1).unwrap_or(&0) as u32) << 8
            | *c.get(2).unwrap_or(&0) as u32;
        for i in 0..4 {
            if i <= c.len() {
                out.push(T[(n >> (18 - 6 * i) & 63) as usize] as char);
            } else {
                out.push('=');
            }
        }
    }
    out
}

pub fn b64_decode(s: &str) -> VzResult<Vec<u8>> {
    let mut out = Vec::new();
    let mut acc = 0u32;
    let mut bits = 0;
    for ch in s.bytes().filter(|c| *c != b'=') {
        let v = match ch {
            b'A'..=b'Z' => ch - b'A',
            b'a'..=b'z' => ch - b'a' + 26,
            b'0'..=b'9' => ch - b'0' + 52,
            b'+' => 62,
            b'/' => 63,
            _ => return Err("bad base64".into()),
        } as u32;
        acc = acc << 6 | v;
        bits += 6;
        if bits >= 8 {
            bits -= 8;
            out.push((acc >> bits & 0xff) as u8);
        }
    }
    Ok(out)
}

fn describe_image(img: &VZMacOSRestoreImage) -> RestoreImage {
    // SAFETY: property reads on a live restore image.
    unsafe {
        let v = img.operatingSystemVersion();
        let req = img.mostFeaturefulSupportedConfiguration();
        RestoreImage {
            url: img
                .URL()
                .absoluteString()
                .map(|s| s.to_string())
                .unwrap_or_default(),
            build: img.buildVersion().to_string(),
            version: format!("{}.{}.{}", v.majorVersion, v.minorVersion, v.patchVersion),
            supported: img.isSupported(),
            hardware_model_b64: req
                .as_ref()
                .map(|r| b64_encode(&r.hardwareModel().dataRepresentation().to_vec())),
            min_cpus: req.as_ref().map(|r| r.minimumSupportedCPUCount()),
            min_memory: req.as_ref().map(|r| r.minimumSupportedMemorySize()),
        }
    }
}

type ImageResult = VzResult<OnQueue<Retained<VZMacOSRestoreImage>>>;

fn image_block(
    tx: mpsc::Sender<ImageResult>,
) -> RcBlock<dyn Fn(*mut VZMacOSRestoreImage, *mut NSError)> {
    RcBlock::new(move |img: *mut VZMacOSRestoreImage, e: *mut NSError| {
        // SAFETY: valid object/error pointers from the framework.
        let r = match (unsafe { Retained::retain(img) }, unsafe { e.as_ref() }) {
            (_, Some(e)) => Err(describe(e)),
            (Some(i), None) => Ok(OnQueue(i)),
            _ => Err("no restore image".into()),
        };
        let _ = tx.send(r);
    })
}

/// Asks Apple which restore image this Mac supports (no download).
pub fn latest_supported() -> VzResult<RestoreImage> {
    let (tx, rx) = mpsc::channel();
    let block = image_block(tx);
    // SAFETY: class method with a live block.
    unsafe { VZMacOSRestoreImage::fetchLatestSupportedWithCompletionHandler(&block) };
    let img = rx
        .recv_timeout(Duration::from_secs(120))
        .map_err(|_| "fetch timed out")??;
    Ok(describe_image(&img.0))
}

/// Reads a local restore image (IPSW).
pub fn load(path: &Path) -> VzResult<RestoreImage> {
    let (tx, rx) = mpsc::channel();
    let block = image_block(tx);
    let url = NSURL::fileURLWithPath(&NSString::from_str(&path.to_string_lossy()));
    // SAFETY: class method with a live block.
    unsafe { VZMacOSRestoreImage::loadFileURL_completionHandler(&url, &block) };
    let img = rx
        .recv_timeout(Duration::from_secs(300))
        .map_err(|_| "load timed out")??;
    Ok(describe_image(&img.0))
}

/// Creates empty auxiliary storage for a hardware model.
pub fn create_aux(path: &Path, hardware_model: &[u8]) -> VzResult<()> {
    // SAFETY: plain initialisers.
    unsafe {
        let hw = VZMacHardwareModel::initWithDataRepresentation(
            VZMacHardwareModel::alloc(),
            &objc2_foundation::NSData::with_bytes(hardware_model),
        )
        .ok_or("invalid hardware model")?;
        VZMacAuxiliaryStorage::initCreatingStorageAtURL_hardwareModel_options_error(
            VZMacAuxiliaryStorage::alloc(),
            &NSURL::fileURLWithPath(&NSString::from_str(&path.to_string_lossy())),
            &hw,
            VZMacAuxiliaryStorageInitializationOptions::empty(),
        )
        .map_err(|e| describe(&e))?;
    }
    Ok(())
}

/// Installs macOS from `ipsw` into the machine described by `cfg`.
/// `progress` receives the fraction completed about once a second.
pub fn install(
    cfg: Retained<VZVirtualMachineConfiguration>,
    ipsw: &Path,
    mut progress: impl FnMut(f64),
) -> VzResult<()> {
    let queue = DispatchQueue::new("si.eilmer.lamp.install", None);
    let (tx, rx) = mpsc::channel::<Option<String>>();
    let (itx, irx) = mpsc::channel::<OnQueue<Retained<VZMacOSInstaller>>>();
    let cfg = OnQueue(cfg);
    let url = OnQueue(NSURL::fileURLWithPath(&NSString::from_str(
        &ipsw.to_string_lossy(),
    )));
    let q2 = OnQueue(queue.clone());
    queue.exec_async(move || {
        let (cfg, url, q2) = (cfg, url, q2);
        // SAFETY: machine and installer are created and driven on the queue.
        unsafe {
            let vm = VZVirtualMachine::initWithConfiguration_queue(
                VZVirtualMachine::alloc(),
                &cfg.0,
                &q2.0,
            );
            let installer = VZMacOSInstaller::initWithVirtualMachine_restoreImageURL(
                VZMacOSInstaller::alloc(),
                &vm,
                &url.0,
            );
            let block = RcBlock::new(move |e: *mut NSError| {
                let _ = tx.send(e.as_ref().map(describe));
            });
            installer.installWithCompletionHandler(&block);
            let _ = itx.send(OnQueue(installer.retain()));
        }
    });
    let installer = irx.recv().map_err(|_| "installer not created")?;
    loop {
        match rx.recv_timeout(Duration::from_secs(1)) {
            Ok(None) => return Ok(()),
            Ok(Some(e)) => return Err(format!("install: {e}")),
            Err(mpsc::RecvTimeoutError::Timeout) => {
                let inst = OnQueue(installer.0.retain());
                let (ptx, prx) = mpsc::channel();
                queue.exec_sync(move || {
                    let inst = inst;
                    // SAFETY: progress read on the installer's queue.
                    let _ = ptx.send(unsafe { inst.0.progress().fractionCompleted() });
                });
                if let Ok(f) = prx.recv() {
                    progress(f);
                }
            }
            Err(e) => return Err(format!("install: {e}")),
        }
    }
}
