#[cfg(target_os = "linux")]
fn main() {
    if let Err(error) = run() {
        eprintln!(
            "{}",
            serde_json::json!({"event": "fixture_failure", "code": error.code})
        );
        std::process::exit(1);
    }
}

#[cfg(not(target_os = "linux"))]
fn main() {
    eprintln!("custody fixture requires Linux and KVM");
    std::process::exit(1);
}

#[cfg(target_os = "linux")]
fn run() -> lamp_engine::Result<()> {
    use lamp_engine::custody::{CapturePurpose, Custody, Destination, Harness, Reconstruction};
    use lamp_engine::engine::{Authority, Config, Engine, Mode};
    use lamp_engine::manifest::sha256_hex;
    use lamp_engine::{EngineError, build};
    use std::path::PathBuf;

    struct MemoryMount {
        path: PathBuf,
        record: PathBuf,
    }
    struct Shutdown<'a>(&'a Engine);
    impl Drop for Shutdown<'_> {
        fn drop(&mut self) {
            self.0.shutdown();
        }
    }
    impl Drop for MemoryMount {
        fn drop(&mut self) {
            use std::os::unix::ffi::OsStrExt;
            let unmounted = std::ffi::CString::new(self.path.as_os_str().as_bytes())
                .is_ok_and(|path| unsafe { libc::umount(path.as_ptr()) } == 0);
            let _ = std::fs::write(
                &self.record,
                if unmounted {
                    b"true\n".as_slice()
                } else {
                    b"false\n".as_slice()
                },
            );
        }
    }

    let args: Vec<String> = std::env::args().skip(1).collect();
    if args.len() != 3 {
        return Err(EngineError::invalid("ROOT SEED SYNTHETIC_NONCE"));
    }
    let root = PathBuf::from(&args[0]);
    let nonce = &args[2];
    if nonce.len() != 32 || !nonce.bytes().all(|b| b.is_ascii_hexdigit()) {
        return Err(EngineError::invalid(
            "synthetic nonce must be 32 hex characters",
        ));
    }
    std::fs::create_dir(&root)?;
    std::fs::write(
        root.join("lamp-disposable-custody-fixture"),
        b"diagnostic\n",
    )?;
    let phase = |value: &str| -> lamp_engine::Result<()> {
        std::fs::write(root.join("phase.json"), serde_json::to_vec(value).unwrap())?;
        Ok(())
    };
    let mem_dir = root.join("memory");
    let memory_mount = MemoryMount {
        path: mem_dir.clone(),
        record: root.join("memory-unmounted.json"),
    };
    let mut cfg = Config::defaults(root.join("store"));
    cfg.mem_dir = mem_dir;
    cfg.mem_dir_size = "6G".into();
    cfg.netns_dir = root.join("netns");
    cfg.uid_base = 2_000_000_000 + u32::from_str_radix(&nonce[..6], 16).unwrap();
    cfg.uid_count = 64;
    phase("open")?;
    let engine = Engine::open(cfg, Mode::Offline)?;
    let shutdown = Shutdown(&engine);
    phase("import_seed")?;
    let seed_ref = build::import_seed(&engine, std::path::Path::new(&args[1]))?;
    let clean = format!(
        "format = \"lamp.template/v1\"\nname = \"custody-clean\"\nguest_os = \"linux\"\n\
         arch = \"x86_64\"\nseed_ref = \"{seed_ref}\"\n\
         [[steps]]\nargv = [\"sh\", \"-c\", \"printf clean-tool > /usr/local/bin/custody-tool\"]\n"
    );
    let clean_digest = sha256_hex(clean.as_bytes());
    let mut graph = Custody::new("pr_fixture", "hs_fixture", ["hs_fixture"])?;
    graph.create("sb_source")?;
    graph.record_reconstruction("sb_source", Reconstruction::verify(&clean, &clean_digest)?)?;
    graph.register_subscription("cr_synthetic", Harness::Codex, ["hs_fixture"])?;
    graph.expose("sb_source", "cr_synthetic")?;
    let canary = format!("lamp-e07-canary-{nonce}");
    let script = format!(
        "import base64, pathlib, time\n\
         token = {canary:?}.encode()\n\
         for directory in ['/var/tmp', '/root/.cache/custody', '/var/log/custody']:\n\
         \x20pathlib.Path(directory).mkdir(parents=True, exist_ok=True)\n\
         \x20for name, data in [('literal', token), ('base64', base64.b64encode(token)), ('hex', token.hex().encode())]:\n\
         \x20\x20pathlib.Path(directory, name).write_bytes(data)\n\
         time.sleep(600)\n"
    );
    let script_base64 = lamp_init::b64_encode(script.as_bytes());
    let shell = format!(
        "printf %s {script_base64} | base64 -d > /var/tmp/custody-resident.py; \
         python3 /var/tmp/custody-resident.py </dev/null >/var/tmp/custody-resident.log 2>&1 & \
         sleep 1; test -s /var/tmp/literal"
    );
    let dirty = format!(
        "{}\n[[steps]]\nargv = {}\n",
        clean.replace("custody-clean", "custody-private"),
        serde_json::to_string(&vec!["sh", "-c", &shell]).unwrap()
    );
    phase("build_source")?;
    let source = build::build_template(&engine, &dirty)?;
    for purpose in [
        CapturePurpose::Template,
        CapturePurpose::Export,
        CapturePurpose::Share,
    ] {
        let error = graph
            .authorize(
                "sb_source",
                Destination {
                    principal: "pr_fixture",
                    host: "hs_fixture",
                    online: true,
                },
                purpose,
            )
            .unwrap_err();
        if error.code != "CLEAN_REBUILD_REQUIRED" {
            return Err(error);
        }
    }
    phase("build_clean")?;
    let clean_report = graph.reconstruction("sb_source")?.build(&engine)?;
    let sandbox_id = format!("sb_t9_{}", &nonce[..10]);
    let auth = Authority {
        sandbox_id: &sandbox_id,
        generation: 1,
        policy_revision: 1,
    };
    phase("allocate_clean")?;
    engine.allocate(auth, None)?;
    phase("restore_clean")?;
    engine.restore(auth, &clean_report.template_id)?;
    phase("useful_command")?;
    let useful = engine.run(
        auth,
        vec![
            "sh".into(),
            "-c".into(),
            "test \"$(cat /usr/local/bin/custody-tool)\" = clean-tool && \
             test ! -e /var/tmp/literal && test ! -e /root/.cache/custody/literal && \
             test ! -e /var/log/custody/literal"
                .into(),
        ],
        std::time::Duration::from_secs(10),
    )?;
    phase("release_clean")?;
    engine.release(auth)?;
    if useful.exit_code != 0 || useful.timed_out {
        return Err(EngineError::host(
            "fresh reconstruction useful command failed",
        ));
    }
    phase("complete")?;
    drop(shutdown);
    println!(
        "{}",
        serde_json::json!({
            "qualification": "diagnostic-engine-api-only",
            "source_template_id": source.template_id,
            "clean_template_id": clean_report.template_id,
            "clean_manifest_digest": clean_report.manifest_digest,
            "definition_digest": clean_digest,
            "unsafe_transfers_denied": 3,
            "fresh_tool_verified": true,
            "seed_ref": seed_ref,
        })
    );
    drop(engine);
    drop(memory_mount);
    Ok(())
}
