//! Confined `lamp.template/v1` builds and seed import. No Docker, OCI image
//! or host container runtime participates: steps run inside a disposable
//! Firecracker build sandbox booted from a verified seed.
use crate::definition::{self, Definition};
use crate::engine::{Authority, Engine, STANDARD};
use crate::error::{EngineError, IoContext, Result};
use crate::fc::{self, Jail, Limits};
use crate::fsutil::{self, now_ms, random_bytes};
use crate::guest;
use crate::manifest::{
    self, AccelerationVariant, Manifest, Object, Provenance, Resources, StepRecord,
};
use lamp_init::{Empty, Exec, GuestRequest, GuestResponse};
use serde::{Deserialize, Serialize};
use std::fmt::Write as _;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SeedObject {
    pub digest: String,
    pub size: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SeedKernel {
    pub digest: String,
    pub size: u64,
    pub source: String,
    pub source_sha256: String,
    pub cmdline: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SeedRootfs {
    pub digest: String,
    pub size: u64,
    pub filesystem: String,
    pub distribution: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Seed {
    pub format: String,
    pub digest_alg: String,
    pub name: String,
    pub guest_os: String,
    pub arch: String,
    pub kernel: SeedKernel,
    pub rootfs: SeedRootfs,
    pub init: std::collections::BTreeMap<String, String>,
    pub packages: SeedObject,
}

fn host_arch() -> &'static str {
    match std::env::consts::ARCH {
        "aarch64" => "arm64",
        a => a,
    }
}

fn check(path: &Path, digest: &str, size: u64, what: &str) -> Result<()> {
    fsutil::verify_object(
        path,
        &Object {
            digest: digest.into(),
            size,
        },
        what,
    )
}

/// Verifies a seed directory: its `seed.json` digest is the seed identity
/// and every object it lists must match.
pub fn verify_seed(dir: &Path, expected_digest: Option<&str>) -> Result<(Seed, String)> {
    let bytes = std::fs::read(dir.join("seed.json")).ctx("read seed.json")?;
    let digest = manifest::sha256_hex(&bytes);
    if expected_digest.is_some_and(|d| d != digest) {
        return Err(EngineError::new(
            "DATA_UNAVAILABLE",
            "seed.json digest does not match seed_ref",
        ));
    }
    let seed: Seed = serde_json::from_slice(&bytes)
        .map_err(|e| EngineError::new("DATA_UNAVAILABLE", format!("seed.json: {e}")))?;
    if seed.format != "lamp.seed/v0" || seed.digest_alg != manifest::DIGEST_ALG {
        return Err(EngineError::invalid("unsupported seed format"));
    }
    check(
        &dir.join(fc::KERNEL),
        &seed.kernel.digest,
        seed.kernel.size,
        "seed kernel",
    )?;
    check(
        &dir.join(fc::ROOTFS),
        &seed.rootfs.digest,
        seed.rootfs.size,
        "seed root",
    )?;
    check(
        &dir.join("packages.txt"),
        &seed.packages.digest,
        seed.packages.size,
        "seed package list",
    )?;
    Ok((seed, digest))
}

/// Imports a built seed into the store after verifying it.
pub fn import_seed(engine: &Engine, src: &Path) -> Result<String> {
    let (seed, digest) = verify_seed(src, None)?;
    let dest = engine.cfg.store.join("seeds").join(&digest);
    if !dest.exists() {
        let tmp = engine.cfg.store.join("staging").join(format!(
            "seed-{}",
            lamp_init::hex_encode(&random_bytes::<6>())
        ));
        std::fs::create_dir_all(&tmp).ctx("create seed staging")?;
        for f in ["seed.json", fc::KERNEL, fc::ROOTFS, "packages.txt"] {
            let status = std::process::Command::new("cp")
                .args(["--sparse=always", "--reflink=auto"])
                .arg(src.join(f))
                .arg(tmp.join(f))
                .status()
                .ctx("copy seed")?;
            if !status.success() {
                return Err(EngineError::host(format!("copy seed object {f}")));
            }
            fsutil::chown(&tmp.join(f), 0, 0)?;
            fsutil::chmod(&tmp.join(f), 0o444)?;
        }
        verify_seed(&tmp, Some(&digest))?;
        std::fs::rename(&tmp, &dest).ctx("publish seed")?;
    }
    Ok(format!("lamp-seed/{}@sha256:{digest}", seed.name))
}

#[derive(Debug, Default)]
pub struct BuildReport {
    pub template_id: String,
    pub manifest_digest: String,
    pub log: String,
}

struct Staging {
    dir: PathBuf,
    done: bool,
}

impl Drop for Staging {
    fn drop(&mut self) {
        if !self.done {
            let _ = std::fs::remove_dir_all(&self.dir);
        }
    }
}

/// Destroys a jail and frees its identity when dropped.
struct JailGuard<'a> {
    engine: &'a Engine,
    jail: Option<Jail>,
}

impl JailGuard<'_> {
    fn get(&mut self) -> &mut Jail {
        self.jail.as_mut().unwrap()
    }
}

impl Drop for JailGuard<'_> {
    fn drop(&mut self) {
        if let Some(j) = self.jail.take() {
            let uid = j.uid;
            j.destroy();
            self.engine.free_uid(uid);
        }
    }
}

fn boot(
    jail: &mut Jail,
    engine: &Engine,
    cmdline: &str,
    vcpus: u32,
    memory_mib: u64,
    nic: bool,
) -> Result<()> {
    jail.spawn(
        &engine.cfg.bins,
        Limits {
            vcpus,
            memory_mib,
            pids_max: 4096,
        },
    )?;
    jail.api(
        "PUT",
        "/boot-source",
        &format!(
            r#"{{"kernel_image_path":"/{}","boot_args":{}}}"#,
            fc::KERNEL,
            fc::js(cmdline)
        ),
    )?;
    jail.api(
        "PUT",
        "/drives/rootfs",
        &format!(r#"{{"drive_id":"rootfs","path_on_host":"/{}","is_root_device":true,"is_read_only":false}}"#, fc::ROOTFS),
    )?;
    jail.api(
        "PUT",
        "/machine-config",
        &format!(
            r#"{{"vcpu_count":{vcpus},"mem_size_mib":{memory_mib},"track_dirty_pages":false}}"#
        ),
    )?;
    jail.api(
        "PUT",
        "/vsock",
        &format!(r#"{{"guest_cid":3,"uds_path":"/{}"}}"#, fc::VSOCK),
    )?;
    if nic {
        // The sandbox link. Template builds have no route or uplink behind
        // it; every sandbox restored from the template gets its own guard.
        crate::net::tap_only(jail)?;
        jail.api(
            "PUT",
            "/network-interfaces/eth0",
            &format!(
                r#"{{"iface_id":"eth0","guest_mac":"{}","host_dev_name":"{}"}}"#,
                lamp_init::GUEST_MAC,
                crate::net::TAP
            ),
        )?;
    }
    jail.api("PUT", "/actions", r#"{"action_type":"InstanceStart"}"#)?;
    guest::wait_ready(&jail.path(fc::VSOCK), Duration::from_secs(60))?;
    Ok(())
}

fn link_ro(src: &Path, dst: &Path) -> Result<()> {
    std::fs::hard_link(src, dst).ctx(format!("link {}", dst.display()))
}

fn seal(path: &Path) -> Result<()> {
    fsutil::chown(path, 0, 0)?;
    fsutil::chmod(path, 0o444)?;
    fsutil::fsync_path(path)
}

/// Builds, verifies, boot-probes and atomically publishes a template.
pub fn build_template(engine: &Engine, definition_text: &str) -> Result<BuildReport> {
    let started = Instant::now();
    let def: Definition = definition::parse(definition_text)?;
    let resources = Resources {
        memory_mib: def.memory_mib.unwrap_or(STANDARD.memory_mib),
        ..STANDARD
    };
    if def.arch != host_arch() {
        return Err(EngineError::new(
            "ENGINE_UNQUALIFIED",
            format!("this host builds {} templates", host_arch()),
        ));
    }
    let seed_ref = definition::parse_seed_ref(&def.seed_ref)?;
    let seed_dir = engine.cfg.store.join("seeds").join(&seed_ref.digest);
    if !seed_dir.exists() {
        return Err(EngineError::not_found(format!("seed {}", def.seed_ref)));
    }
    let (seed, _) = verify_seed(&seed_dir, Some(&seed_ref.digest))?;
    if seed.name != seed_ref.name || seed.arch != def.arch || seed.guest_os != def.guest_os {
        return Err(EngineError::invalid(
            "seed does not match the definition's name/arch/guest_os",
        ));
    }
    let tag = lamp_init::hex_encode(&random_bytes::<5>());
    let mut staging = Staging {
        dir: engine
            .cfg
            .store
            .join("staging")
            .join(format!("build-{tag}")),
        done: false,
    };
    std::fs::create_dir_all(&staging.dir).ctx("create staging")?;
    let st = staging.dir.clone();
    let mut log = String::new();
    let _ = writeln!(
        log,
        "definition: {} ({} steps), seed {}",
        def.name,
        def.steps.len(),
        def.seed_ref
    );

    // 1. Build sandbox from the seed; run steps with no network.
    let mut steps = Vec::new();
    {
        let mut b = JailGuard {
            engine,
            jail: Some(engine.new_jail(&format!("bld-{tag}"))?),
        };
        let uid = b.get().uid;
        link_ro(&seed_dir.join(fc::KERNEL), &b.get().path(fc::KERNEL))?;
        fsutil::reflink(&seed_dir.join(fc::ROOTFS), &b.get().path(fc::ROOTFS), 0o600)?;
        fsutil::chown(&b.get().path(fc::ROOTFS), uid, uid)?;
        let t = Instant::now();
        boot(b.get(), engine, &seed.kernel.cmdline, 2, 2048, false)?;
        let _ = writeln!(
            log,
            "build sandbox booted in {} ms",
            t.elapsed().as_millis()
        );
        let vsock = b.get().path(fc::VSOCK);
        let build_id = format!("sb_build_{tag}");
        engine.barrier(&vsock, &build_id, 1)?;
        for (i, step) in def.steps.iter().enumerate() {
            let t = Instant::now();
            let timeout_ms = step.timeout_s.unwrap_or(600) * 1000;
            let exec_id = format!("step_{i}");
            let mut conn = guest::send(
                &vsock,
                &GuestRequest::Exec(Exec {
                    sandbox_id: build_id.clone(),
                    generation: 1,
                    exec_id: exec_id.clone(),
                    argv: step.argv.clone(),
                    env: step.env.clone(),
                    cwd: step.cwd.clone().unwrap_or_default(),
                    timeout_ms,
                    output_limit: lamp_init::DEFAULT_OUTPUT_LIMIT,
                    stdin_path: String::new(),
                    pty: false,
                    stdin_open: false,
                    audit: None,
                }),
            )?;
            match conn.next(Some(Duration::from_secs(10)))? {
                GuestResponse::ExecStarted(_) => {}
                other => return Err(EngineError::host(format!("step {i}: {other:?}"))),
            }
            let done = guest::wait_exec(
                &vsock,
                &build_id,
                1,
                &exec_id,
                Some(Instant::now() + Duration::from_millis(timeout_ms + 5000)),
                || true,
            )?;
            let out = String::from_utf8_lossy(
                &lamp_init::b64_decode(&done.stdout_b64).unwrap_or_default(),
            )
            .into_owned();
            let err = String::from_utf8_lossy(
                &lamp_init::b64_decode(&done.stderr_b64).unwrap_or_default(),
            )
            .into_owned();
            let _ = writeln!(
                log,
                "step {i} {:?}: exit {} in {} ms\n{out}{err}",
                step.argv,
                done.exit_code,
                t.elapsed().as_millis()
            );
            if done.exit_code != 0 || done.timed_out {
                let failed = engine.cfg.store.join("failed");
                let _ = std::fs::create_dir_all(&failed);
                let _ = std::fs::write(failed.join(format!("build-{tag}.log")), &log);
                return Err(EngineError::invalid(format!(
                    "step {i} failed (exit {}, timed_out {}); log kept in failed/build-{tag}.log",
                    done.exit_code, done.timed_out
                )));
            }
            steps.push(StepRecord {
                argv: step.argv.clone(),
                exit_code: done.exit_code,
                elapsed_ms: t.elapsed().as_millis() as u64,
            });
        }
        match guest::call(
            &vsock,
            &GuestRequest::Shutdown(Empty {}),
            Duration::from_secs(10),
        )? {
            GuestResponse::ShuttingDown => {}
            other => return Err(EngineError::host(format!("shutdown: {other:?}"))),
        }
        if !b.get().wait_exit(Duration::from_secs(60)) {
            return Err(EngineError::host("build sandbox did not power off"));
        }
        std::fs::rename(b.get().path(fc::ROOTFS), st.join("root.ext4"))
            .ctx("collect built root")?;
    }
    fsutil::reflink(&seed_dir.join(fc::KERNEL), &st.join(fc::KERNEL), 0o444)?;
    seal(&st.join("root.ext4"))?;
    seal(&st.join(fc::KERNEL))?;

    // 2. Boot the built root at the standard size and capture the
    //    acceleration variant (full RAM/CPU/device state) while idle.
    {
        let mut v = JailGuard {
            engine,
            jail: Some(engine.new_jail(&format!("tpl-{tag}"))?),
        };
        let uid = v.get().uid;
        link_ro(&st.join(fc::KERNEL), &v.get().path(fc::KERNEL))?;
        fsutil::reflink(&st.join("root.ext4"), &v.get().path(fc::ROOTFS), 0o600)?;
        fsutil::chown(&v.get().path(fc::ROOTFS), uid, uid)?;
        let t = Instant::now();
        boot(
            v.get(),
            engine,
            &seed.kernel.cmdline,
            STANDARD.vcpus,
            resources.memory_mib,
            true,
        )?;
        let _ = writeln!(log, "template VM booted in {} ms", t.elapsed().as_millis());
        // Let boot-time work settle so the captured guest is idle. Like
        // every capture, the template is taken with task processes frozen;
        // each sandbox's identity barrier thaws them.
        std::thread::sleep(Duration::from_millis(500));
        match guest::call(
            &v.get().path(fc::VSOCK),
            &GuestRequest::Freeze(Empty {}),
            Duration::from_secs(10),
        )? {
            GuestResponse::Frozen => {}
            other => return Err(EngineError::host(format!("freeze: {other:?}"))),
        }
        for f in [fc::MEM, fc::VMSTATE] {
            std::fs::File::create(v.get().path(f)).ctx("create capture file")?;
            fsutil::chown(&v.get().path(f), uid, uid)?;
        }
        v.get().api("PATCH", "/vm", r#"{"state":"Paused"}"#)?;
        let t = Instant::now();
        v.get().api(
            "PUT",
            "/snapshot/create",
            &format!(
                r#"{{"snapshot_type":"Full","mem_file_path":"/{}","snapshot_path":"/{}","sync_snapshot_files":true}}"#,
                fc::MEM,
                fc::VMSTATE
            ),
        )?;
        let _ = writeln!(
            log,
            "full capture written in {} ms",
            t.elapsed().as_millis()
        );
        v.get().kill();
        for f in [fc::MEM, fc::VMSTATE, fc::ROOTFS] {
            std::fs::rename(v.get().path(f), st.join(f)).ctx("collect variant")?;
        }
    }
    let punched = fsutil::sparsify(&st.join(fc::MEM))?;
    let _ = writeln!(
        log,
        "memory image: {} MiB of zero pages left as holes",
        punched >> 20
    );
    for f in [fc::MEM, fc::VMSTATE, fc::ROOTFS] {
        seal(&st.join(f))?;
    }

    // 3. Manifest and signed provenance.
    let kernel = fsutil::digest_file(&st.join(fc::KERNEL))?;
    let compat = engine.compatibility(&kernel.digest);
    let manifest = Manifest {
        format: definition::FORMAT.into(),
        digest_alg: manifest::DIGEST_ALG.into(),
        name: def.name.clone(),
        guest_os: def.guest_os.clone(),
        arch: def.arch.clone(),
        seed_ref: def.seed_ref.clone(),
        definition_sha256: manifest::sha256_hex(definition_text.as_bytes()),
        package_lock_blob_id: def.package_lock_blob_id.clone(),
        network: def.network.clone(),
        kernel_cmdline: seed.kernel.cmdline.clone(),
        root: fsutil::digest_file(&st.join("root.ext4"))?,
        kernel,
        steps,
        built_at_unix_ms: now_ms(),
        builder: format!(
            "lamp-engine {} on {}",
            env!("CARGO_PKG_VERSION"),
            engine.cfg.host_id
        ),
        acceleration_variants: vec![AccelerationVariant {
            compatibility: compat.clone(),
            resources: resources.clone(),
            memory: fsutil::digest_file(&st.join(fc::MEM))?,
            vmstate: fsutil::digest_file(&st.join(fc::VMSTATE))?,
            disk: fsutil::digest_file(&st.join(fc::ROOTFS))?,
        }],
    };
    let digest = manifest.digest()?;
    let mut provenance = Provenance {
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
    engine.sign_provenance(&mut provenance)?;
    std::fs::write(
        st.join("manifest.json"),
        manifest::canonical_json(&manifest)?,
    )
    .ctx("write manifest")?;
    std::fs::write(
        st.join("provenance.json"),
        manifest::canonical_json(&provenance)?,
    )
    .ctx("write provenance")?;
    std::fs::write(st.join("build.log"), &log).ctx("write build log")?;
    for f in ["manifest.json", "provenance.json", "build.log"] {
        seal(&st.join(f))?;
    }
    fsutil::fsync_path(&st)?;

    // 4. Publish by digest, verify everything, boot-probe, then move the name.
    let id = manifest::template_id(&digest);
    let dest = engine.cfg.store.join("templates").join(&id);
    if dest.exists() {
        return Err(EngineError::in_use(format!(
            "template {id} already published"
        )));
    }
    std::fs::rename(&st, &dest).ctx("publish template")?;
    staging.done = true;
    let template = match engine.verify_template_dir(&dest) {
        Ok(t) => t,
        Err(e) => {
            quarantine(engine, &dest, &id);
            return Err(e.context("verify published template"));
        }
    };
    engine.register_template(template);
    if let Err(e) = probe(engine, &id, &tag, &resources) {
        engine.unregister_template(&id);
        quarantine(engine, &dest, &id);
        return Err(e.context("boot probe"));
    }
    let names = engine.cfg.store.join("templates/names");
    let tmp = names.join(format!(".{}.{tag}", def.name));
    std::os::unix::fs::symlink(format!("../{id}"), &tmp).ctx("name template")?;
    std::fs::rename(&tmp, names.join(&def.name)).ctx("name template")?;
    let _ = writeln!(
        log,
        "published {id} as {} in {} ms",
        def.name,
        started.elapsed().as_millis()
    );
    Ok(BuildReport {
        template_id: id,
        manifest_digest: digest,
        log,
    })
}

fn quarantine(engine: &Engine, dir: &Path, id: &str) {
    let q = engine.cfg.store.join("quarantine");
    let _ = std::fs::create_dir_all(&q);
    let _ = std::fs::rename(dir, q.join(id));
}

/// Creates a real sandbox from the new template and runs a command.
fn probe(engine: &Engine, template_id: &str, tag: &str, resources: &Resources) -> Result<()> {
    let sid = format!("sb_probe_{tag}");
    let auth = Authority {
        sandbox_id: &sid,
        generation: 1,
        policy_revision: 0,
    };
    engine.allocate(auth, Some(resources.clone()))?;
    let result = engine.restore_build_probe(auth, template_id).and_then(|_| {
        engine.run(
            auth,
            vec![
                "sh".into(),
                "-c".into(),
                "uname -sr && test -x /sbin/lamp-init".into(),
            ],
            Duration::from_secs(10),
        )
    });
    let _ = engine.release(auth);
    match result? {
        c if c.exit_code == 0 => Ok(()),
        other => Err(EngineError::host(format!(
            "probe command failed: {other:?}"
        ))),
    }
}
