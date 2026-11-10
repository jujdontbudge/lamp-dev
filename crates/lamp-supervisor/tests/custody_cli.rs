#![cfg(target_os = "linux")]

use std::path::PathBuf;
use std::process::Command;
use std::time::{SystemTime, UNIX_EPOCH};

struct Scratch(PathBuf);

impl Scratch {
    fn new() -> Self {
        let path = std::env::temp_dir().join(format!(
            "lamp-custody-cli-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        std::fs::create_dir(&path).unwrap();
        Self(path)
    }
}

impl Drop for Scratch {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}

#[test]
fn rebuild_without_nonsecret_approval_has_no_store_effect() {
    let scratch = Scratch::new();
    let store = scratch.0.join("store");
    let output = Command::new(env!("CARGO_BIN_EXE_lamp-supervisor"))
        .arg("template-rebuild")
        .arg("--store")
        .arg(&store)
        .arg(scratch.0.join("absent.lamp"))
        .output()
        .unwrap();
    assert_eq!(output.status.code(), Some(1));
    assert!(String::from_utf8_lossy(&output.stderr).contains("--approved-nonsecret-digest"));
    assert!(!store.exists());
}

#[test]
fn changed_reconstruction_input_has_no_store_effect() {
    let scratch = Scratch::new();
    let store = scratch.0.join("store");
    let definition = scratch.0.join("changed.lamp");
    std::fs::write(&definition, "changed-after-approval").unwrap();
    let output = Command::new(env!("CARGO_BIN_EXE_lamp-supervisor"))
        .arg("template-rebuild")
        .arg("--store")
        .arg(&store)
        .arg(definition)
        .arg("--approved-nonsecret-digest")
        .arg("0".repeat(64))
        .output()
        .unwrap();
    assert_eq!(output.status.code(), Some(1));
    assert!(String::from_utf8_lossy(&output.stderr).contains("CLEAN_REBUILD_REQUIRED"));
    assert!(!store.exists());
}
