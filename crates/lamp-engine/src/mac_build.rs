//! Mac template builds.
//!
//! - Linux: boot the arm64 seed in a confined worker, pass the identity
//!   barrier and a command probe, then pause and save the full machine as
//!   the template's signed acceleration variant (prepared restore).
//! - macOS: import an owner-installed machine (`lamp-vz install` from the
//!   owner's restore image), boot it fresh in a confined worker until the
//!   guest helper answers, run `sw_vers`, shut it down cleanly and publish
//!   the disk and boot storage. Nothing is redistributed: the template
//!   exists only on the owner's Mac.
use super::*;

pub struct Built {
    pub template_id: String,
    pub manifest_digest: String,
    pub log: String,
}

fn staging_sandbox(engine: &Engine, tag: &str, resources: Resources, guest_os: &str) -> Sandbox {
    let id = format!(
        "sb_build_{tag}_{}",
        lamp_init::hex_encode(&random_bytes::<4>())
    );
    Sandbox {
        dir: engine.cfg.store.join("sb").join(&id),
        id,
        generation: 1,
        policy_revision: 1,
        resources,
        state: State::Allocated,
        guest_os: guest_os.into(),
        identity: None,
        worker: None,
        machine: None,
        source: String::new(),
        captures: Vec::new(),
        barrier: None,
        commands: Arc::default(),
        last_start: BTreeMap::new(),
    }
}

fn cleanup(engine: &Engine, sb: &mut Sandbox) {
    if let Some(mut w) = sb.worker.take() {
        w.kill();
    }
    if let Some(id) = sb.identity.take() {
        engine.free_identity(&id);
    }
    let _ = std::fs::remove_dir_all(&sb.dir);
    if let Some(r) = engine.ram_dir(&sb.id) {
        let _ = std::fs::remove_dir_all(r);
    }
}

fn exec(engine: &Engine, sb: &Sandbox, argv: &[&str], log: &mut String) -> Result<ExecDone> {
    let w = Engine::worker(sb)?;
    let op = format!("op_build_{}", lamp_init::hex_encode(&random_bytes::<6>()));
    let mut conn = guest::send(
        &w.vsock,
        &GuestRequest::Exec(Exec {
            sandbox_id: sb.id.clone(),
            generation: sb.generation,
            exec_id: op.clone(),
            argv: argv.iter().map(|s| s.to_string()).collect(),
            env: BTreeMap::new(),
            cwd: String::new(),
            timeout_ms: 120_000,
            output_limit: 64 << 10,
            audit: None,
        }),
    )?;
    match conn.next(Some(Duration::from_secs(30)))? {
        GuestResponse::ExecStarted(_) => {}
        other => return Err(EngineError::host(format!("probe exec: {other:?}"))),
    }
    let d = guest::wait_exec(
        &w.vsock,
        &sb.id,
        sb.generation,
        &op,
        Some(Instant::now() + Duration::from_secs(130)),
        w.liveness(),
    )?;
    let out = lamp_init::b64_decode(&d.stdout_b64).unwrap_or_default();
    log.push_str(&format!(
        "$ {}\n{}",
        argv.join(" "),
        String::from_utf8_lossy(&out)
    ));
    let _ = engine;
    Ok(d)
}

fn publish(
    engine: &Engine,
    staging: &Path,
    manifest: &Manifest,
    compat: &Compatibility,
    name: &str,
) -> Result<(String, String)> {
    let digest = manifest.digest()?;
    let mut prov = Provenance {
        producer_key_id: String::new(),
        producer_host_id: engine.cfg.host_id.clone(),
        owner_id: "local".into(),
        trust_class: "owner".into(),
        manifest_digest: digest.clone(),
        engine_digest: compat.engine_digest.clone(),
        guest_kernel_digest: compat.guest_kernel_digest.clone(),
        device_graph_digest: compat.device_graph_digest.clone(),
        cpu_baseline: compat.cpu_baseline.clone(),
        signature: String::new(),
    };
    engine.sign_provenance(&mut prov)?;
    std::fs::write(
        staging.join("manifest.json"),
        manifest::canonical_json(manifest)?,
    )
    .ctx("write manifest")?;
    std::fs::write(
        staging.join("provenance.json"),
        manifest::canonical_json(&prov)?,
    )
    .ctx("write provenance")?;
    // Template objects are immutable and readable by every worker identity.
    for e in std::fs::read_dir(staging).ctx("list staging")?.flatten() {
        fsutil::chown(&e.path(), 0, 0)?;
        fsutil::chmod(&e.path(), 0o444)?;
        fsutil::full_fsync(&e.path())?;
    }
    fsutil::chown(staging, 0, 0)?;
    fsutil::chmod(staging, 0o755)?;
    let id = manifest::template_id(&digest);
    let dst = engine.cfg.store.join("templates").join(&id);
    if dst.exists() {
        let _ = std::fs::remove_dir_all(staging);
    } else {
        std::fs::rename(staging, &dst).ctx("publish template")?;
        fsutil::full_fsync(&engine.cfg.store.join("templates"))?;
    }
    let link = engine.cfg.store.join("templates/names").join(name);
    let tmp = link.with_extension("tmp");
    let _ = std::fs::remove_file(&tmp);
    std::os::unix::fs::symlink(&dst, &tmp).ctx("name template")?;
    std::fs::rename(&tmp, &link).ctx("name template")?;
    let t = engine.verify_template_dir(&dst)?;
    engine.register_template(t);
    Ok((id, digest))
}

impl Engine {
    /// Builds a prepared Linux template from an arm64 seed directory
    /// (`Image`, `rootfs.ext4`, `seed.json` from build-seed-arm64.sh).
    pub fn build_linux_template(
        &self,
        seed: &Path,
        name: &str,
        resources: Resources,
    ) -> Result<Built> {
        let mut log = String::new();
        let seed_json: serde_json::Value =
            serde_json::from_slice(&std::fs::read(seed.join("seed.json")).ctx("read seed.json")?)
                .map_err(|e| EngineError::invalid(format!("seed.json: {e}")))?;
        if seed_json["arch"] != "arm64" || seed_json["guest_os"] != "linux" {
            return Err(EngineError::new(
                "ENGINE_UNQUALIFIED",
                "seed is not a Linux arm64 seed",
            ));
        }
        for (f, key) in [("Image", "kernel"), ("rootfs.ext4", "rootfs")] {
            let want = seed_json[key]["sha256"].as_str().unwrap_or("");
            let got = sha256_file(&seed.join(f))?;
            if got != want {
                return Err(EngineError::new(
                    "DATA_UNAVAILABLE",
                    format!("seed {f} does not match seed.json"),
                ));
            }
        }
        let cmdline = seed_json["kernel"]["cmdline"]
            .as_str()
            .unwrap_or("console=hvc0 root=/dev/vda rw init=/sbin/lamp-init")
            .to_string();
        let staging = self.cfg.store.join("staging").join(format!(
            "tpl_{}",
            lamp_init::hex_encode(&random_bytes::<6>())
        ));
        std::fs::create_dir_all(&staging).ctx("create staging")?;
        fsutil::reflink(&seed.join("Image"), &staging.join(KERNEL), 0o444)?;
        fsutil::reflink(&seed.join("rootfs.ext4"), &staging.join("root.ext4"), 0o444)?;
        let mut sb = staging_sandbox(self, "linux", resources.clone(), "linux");
        let r = (|| -> Result<Built> {
            self.new_dir(&mut sb)?;
            let f = std::fs::File::create(sb.dir.join(DISK)).ctx("create disk")?;
            drop(f);
            std::fs::remove_file(sb.dir.join(DISK)).ok();
            fsutil::reflink(&seed.join("rootfs.ext4"), &sb.dir.join(DISK), 0o600)?;
            let disk_len = std::fs::metadata(sb.dir.join(DISK))?.len();
            if disk_len < resources.disk_gib << 30 {
                std::fs::OpenOptions::new()
                    .write(true)
                    .open(sb.dir.join(DISK))?
                    .set_len(resources.disk_gib << 30)?;
            }
            let mid = new_generic_machine_identifier(&self.cfg.worker)?;
            let spec = WorkerSpec {
                devices: Default::default(),
                sandbox_id: sb.id.clone(),
                machine: Machine::Linux {
                    kernel: staging.join(KERNEL),
                    initrd: None,
                    cmdline: cmdline.clone(),
                    machine_identifier_b64: mid.clone(),
                },
                cpus: resources.vcpus as usize,
                memory_mib: resources.memory_mib,
                disks: vec![DiskSpec {
                    path: sb.dir.join(DISK),
                    read_only: false,
                    sync: DiskSync::Full,
                }],
                dir: sb.dir.clone(),
                nat_network: false,
                read_paths: vec![],
                write_paths: vec![],
                write_dirs: vec![],
            };
            // The staging kernel must be readable by the build identity.
            fsutil::chmod(&staging, 0o755)?;
            let mut detail = BTreeMap::new();
            let t = Instant::now();
            self.spawn(&mut sb, spec, None, &mut detail)?;
            self.finish_start(&mut sb, false, &mut detail)?;
            log.push_str(&format!(
                "booted in {} ms: {detail:?}\n",
                t.elapsed().as_millis()
            ));
            let probe = exec(
                self,
                &sb,
                &[
                    "sh",
                    "-c",
                    "uname -m && python3 --version && cc --version | head -1 && getconf PAGESIZE",
                ],
                &mut log,
            )?;
            if probe.exit_code != 0 {
                return Err(EngineError::host("template probe failed"));
            }
            let w = Engine::worker(&sb)?;
            let vsock = w.vsock.clone();
            if let Err((e, _)) = freeze_guest(&vsock) {
                return Err(e);
            }
            w.call(&Api::Pause {}, Duration::from_secs(60))?;
            let state = sb.dir.join(STATE_FILE);
            w.call(
                &Api::Save {
                    path: state.clone(),
                },
                Duration::from_secs(900),
            )?;
            // Disk at exactly the saved instant.
            fsutil::reflink(&sb.dir.join(DISK), &staging.join(DISK), 0o444)?;
            std::fs::rename(&state, staging.join(STATE_FILE)).ctx("take template state")?;
            let machine = MachineCfg::Linux {
                cmdline: cmdline.clone(),
                machine_identifier_b64: mid,
                resources: resources.clone(),
            };
            std::fs::write(staging.join(MACHINE), serde_json::to_vec(&machine).unwrap())
                .ctx("write machine")?;
            cleanup(self, &mut sb);
            let kernel = object(&staging.join(KERNEL))?;
            let compat = self.compatibility(&kernel.digest, DEVICE_GRAPH_LINUX);
            let manifest = Manifest {
                format: "lamp.template/v1".into(),
                digest_alg: manifest::DIGEST_ALG.into(),
                name: name.into(),
                guest_os: "linux".into(),
                arch: "arm64".into(),
                seed_ref: format!(
                    "lamp-seed/{}@sha256:{}",
                    seed_json["name"].as_str().unwrap_or("seed"),
                    sha256_file(&seed.join("seed.json"))?
                ),
                definition_sha256: manifest::sha256_hex(
                    format!("prepare {name} {resources:?}").as_bytes(),
                ),
                package_lock_blob_id: None,
                network: "none".into(),
                kernel,
                kernel_cmdline: cmdline.clone(),
                root: object(&staging.join("root.ext4"))?,
                steps: vec![],
                built_at_unix_ms: now_ms(),
                builder: format!(
                    "lamp-supervisor {} on {}",
                    env!("CARGO_PKG_VERSION"),
                    self.cfg.host_id
                ),
                acceleration_variants: vec![AccelerationVariant {
                    compatibility: compat.clone(),
                    resources: resources.clone(),
                    memory: object(&staging.join(STATE_FILE))?,
                    vmstate: object(&staging.join(MACHINE))?,
                    disk: object(&staging.join(DISK))?,
                }],
            };
            let (id, digest) = publish(self, &staging, &manifest, &compat, name)?;
            Ok(Built {
                template_id: id,
                manifest_digest: digest,
                log: log.clone(),
            })
        })();
        cleanup(self, &mut sb);
        if r.is_err() {
            let _ = std::fs::remove_dir_all(&staging);
        }
        r
    }

    /// Imports an owner-installed macOS machine directory (`disk.img`,
    /// `aux.img`, `machine.json` and `restore-image.json` written by
    /// `lamp-vz install`) as an owner template after a fresh-boot probe.
    pub fn import_macos_template(
        &self,
        vm: &Path,
        name: &str,
        minimum: Resources,
        default: Resources,
    ) -> Result<Built> {
        let mut log = String::new();
        let installed: lamp_vz::spec::WorkerSpec = serde_json::from_slice(
            &std::fs::read(vm.join("machine.json")).ctx("read machine.json")?,
        )
        .map_err(|e| EngineError::invalid(format!("machine.json: {e}")))?;
        let image: serde_json::Value = serde_json::from_slice(
            &std::fs::read(vm.join("restore-image.json")).ctx("read restore-image.json")?,
        )
        .map_err(|e| EngineError::invalid(format!("restore-image.json: {e}")))?;
        let Machine::Macos {
            hardware_model_b64, ..
        } = &installed.machine
        else {
            return Err(EngineError::invalid("not a macOS machine"));
        };
        let staging = self.cfg.store.join("staging").join(format!(
            "tpl_{}",
            lamp_init::hex_encode(&random_bytes::<6>())
        ));
        std::fs::create_dir_all(&staging).ctx("create staging")?;
        let mut sb = staging_sandbox(self, "macos", default.clone(), "macos");
        let r = (|| -> Result<Built> {
            self.new_dir(&mut sb)?;
            fsutil::reflink(&vm.join(DISK), &sb.dir.join(DISK), 0o600)?;
            fsutil::reflink(&vm.join(AUX), &sb.dir.join(AUX), 0o600)?;
            let spec = WorkerSpec {
                devices: Default::default(),
                sandbox_id: sb.id.clone(),
                machine: Machine::Macos {
                    hardware_model_b64: hardware_model_b64.clone(),
                    machine_identifier_b64: new_mac_machine_identifier(&self.cfg.worker)?,
                    auxiliary_storage: sb.dir.join(AUX),
                },
                cpus: default.vcpus as usize,
                memory_mib: default.memory_mib,
                disks: vec![DiskSpec {
                    path: sb.dir.join(DISK),
                    read_only: false,
                    sync: DiskSync::Full,
                }],
                dir: sb.dir.clone(),
                nat_network: false,
                read_paths: vec![],
                write_paths: vec![],
                write_dirs: vec![],
            };
            let mut detail = BTreeMap::new();
            let t = Instant::now();
            self.spawn(&mut sb, spec, None, &mut detail)?;
            self.finish_start(&mut sb, false, &mut detail)?;
            log.push_str(&format!(
                "booted to the guest helper in {} ms\n",
                t.elapsed().as_millis()
            ));
            let probe = exec(self, &sb, &["/usr/bin/sw_vers"], &mut log)?;
            if probe.exit_code != 0 {
                return Err(EngineError::host("macOS probe failed"));
            }
            // Clean shutdown, then publish exactly that disk and boot storage.
            let w = Engine::worker(&sb)?;
            let _ = guest::call(
                &w.vsock,
                &GuestRequest::Shutdown(Empty {}),
                Duration::from_secs(10),
            );
            let deadline = Instant::now() + Duration::from_secs(300);
            while sb.worker.as_mut().is_some_and(Worker::alive) && Instant::now() < deadline {
                std::thread::sleep(Duration::from_millis(500));
            }
            if sb.worker.as_mut().is_some_and(Worker::alive) {
                return Err(EngineError::host("macOS guest did not shut down"));
            }
            log.push_str(&format!(
                "clean shutdown after {} ms\n",
                t.elapsed().as_millis()
            ));
            fsutil::reflink(&sb.dir.join(DISK), &staging.join(DISK), 0o444)?;
            fsutil::reflink(&sb.dir.join(AUX), &staging.join(AUX), 0o444)?;
            let machine = MachineCfg::Macos {
                hardware_model_b64: hardware_model_b64.clone(),
                minimum_resources: minimum.clone(),
                default_resources: default.clone(),
                os_version: image["version"].as_str().unwrap_or("").into(),
                os_build: image["build"].as_str().unwrap_or("").into(),
            };
            std::fs::write(staging.join(MACHINE), serde_json::to_vec(&machine).unwrap())
                .ctx("write machine")?;
            std::fs::write(
                staging.join("restore-image.json"),
                serde_json::to_vec(&image).unwrap(),
            )
            .ctx("write image")?;
            cleanup(self, &mut sb);
            let aux = object(&staging.join(AUX))?;
            let compat = self.compatibility(&aux.digest, DEVICE_GRAPH_MACOS);
            let manifest = Manifest {
                format: "lamp.template/v1".into(),
                digest_alg: manifest::DIGEST_ALG.into(),
                name: name.into(),
                guest_os: "macos".into(),
                arch: "arm64".into(),
                seed_ref: format!(
                    "owner-media/macos-{}-{}@{}",
                    image["version"].as_str().unwrap_or(""),
                    image["build"].as_str().unwrap_or(""),
                    image["url"].as_str().unwrap_or("")
                ),
                definition_sha256: manifest::sha256_hex(
                    format!("import {name} {minimum:?} {default:?}").as_bytes(),
                ),
                package_lock_blob_id: None,
                network: "none".into(),
                kernel: aux,
                kernel_cmdline: String::new(),
                root: object(&staging.join(DISK))?,
                steps: vec![],
                built_at_unix_ms: now_ms(),
                builder: format!(
                    "lamp-supervisor {} on {}",
                    env!("CARGO_PKG_VERSION"),
                    self.cfg.host_id
                ),
                acceleration_variants: vec![],
            };
            let (id, digest) = publish(self, &staging, &manifest, &compat, name)?;
            Ok(Built {
                template_id: id,
                manifest_digest: digest,
                log: log.clone(),
            })
        })();
        cleanup(self, &mut sb);
        if r.is_err() {
            let _ = std::fs::remove_dir_all(&staging);
        }
        r
    }
}

fn sha256_file(p: &Path) -> Result<String> {
    use sha2::{Digest, Sha256};
    use std::io::Read;
    let mut f = std::fs::File::open(p).ctx(format!("open {}", p.display()))?;
    let mut h = Sha256::new();
    let mut buf = vec![0u8; 1 << 20];
    loop {
        let n = f.read(&mut buf)?;
        if n == 0 {
            break;
        }
        h.update(&buf[..n]);
    }
    Ok(lamp_init::hex_encode(&h.finalize()))
}
