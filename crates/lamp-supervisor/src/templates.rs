//! Template management over the enrolled worker grant (never the root admin socket).
use lamp_engine::{
    EngineError, Result, definition,
    engine::{Engine, Outcome},
};
use lamp_worker_protocol::Template;
use serde_json::json;

pub fn dispatch(engine: &Engine, request: &Template) -> Result<Outcome> {
    let mut out = Outcome {
        state: "templates".into(),
        ..Default::default()
    };
    match request.action.as_str() {
        "list" => {
            let items: Vec<_> = engine.templates().iter().map(|t| {
                #[cfg(target_os = "linux")]
                let (resources, fixed) = (&t.variant.resources, false);
                #[cfg(target_os = "macos")]
                let (resources, fixed) = match &t.machine {
                    lamp_engine::engine::MachineCfg::Linux { resources, .. } => (resources, false),
                    lamp_engine::engine::MachineCfg::Macos { default_resources, .. } => (default_resources, false),
                };
                #[cfg(target_os = "linux")]
                let minimum = json!({"vcpus": 1, "memory_mib": 256, "disk_gib": resources.disk_gib});
                #[cfg(target_os = "macos")]
                let minimum = match &t.machine {
                    lamp_engine::engine::MachineCfg::Linux { resources, .. } => json!({"vcpus": 1, "memory_mib": 256, "disk_gib": resources.disk_gib}),
                    lamp_engine::engine::MachineCfg::Macos { minimum_resources, .. } => json!(minimum_resources),
                };
                json!({"id": t.id, "name": t.manifest.name, "arch": t.manifest.arch,
                    "guest_os": t.manifest.guest_os, "seed_ref":t.manifest.seed_ref, "resources": resources, "minimum_resources": minimum, "fixed_resources": fixed})
            }).collect();
            out.detail
                .insert("items".into(), serde_json::to_string(&items).unwrap());
        }
        "log" => {
            #[cfg(target_os = "linux")]
            let definition = request.definition.clone();
            #[cfg(target_os = "macos")]
            let definition = definition::parse(&request.definition)
                .ok()
                .and_then(|d| lamp_engine::definition::normalized(&d))
                .unwrap_or_else(|| request.definition.clone());
            out.detail.insert(
                "log".into(),
                lamp_engine::buildlog::read(&engine.cfg.store, &definition),
            );
        }
        "check" => {
            let v: serde_json::Value = serde_json::from_str(&request.definition)
                .map_err(|e| EngineError::invalid(e.to_string()))?;
            let cpus = v["vcpus"].as_u64().unwrap_or(0);
            let memory = v["memory_mib"].as_u64().unwrap_or(0);
            let disk = v["disk_gib"].as_u64().unwrap_or(0);
            let max_cpu = std::thread::available_parallelism()
                .map(|n| n.get() as u64)
                .unwrap_or(1);
            if cpus == 0 || cpus > max_cpu {
                return Err(EngineError::invalid(format!("vcpus must be 1..={max_cpu}")));
            }
            #[cfg(target_os = "linux")]
            let max_memory = lamp_engine::engine::HostCapacity::detect(&engine.cfg.store)?
                .physical_mib
                .saturating_sub(2048);
            #[cfg(target_os = "macos")]
            let max_memory = (std::process::Command::new("/usr/sbin/sysctl")
                .args(["-n", "hw.memsize"])
                .output()
                .ok()
                .and_then(|o| {
                    String::from_utf8_lossy(&o.stdout)
                        .trim()
                        .parse::<u64>()
                        .ok()
                })
                .unwrap_or(0)
                / (1 << 20))
                .saturating_sub(2048);
            if memory < 256 || memory > max_memory {
                return Err(EngineError::invalid(format!(
                    "memory_mib must be 256..={max_memory} (host reserve excluded)"
                )));
            }
            let t = engine.template(&request.reference)?;
            #[cfg(target_os = "linux")]
            if disk < t.variant.resources.disk_gib || disk > 65536 {
                return Err(EngineError::invalid(format!(
                    "disk_gib must be {}..=65536",
                    t.variant.resources.disk_gib
                )));
            }
            #[cfg(target_os = "macos")]
            match &t.machine {
                lamp_engine::engine::MachineCfg::Linux { resources, .. } => {
                    if disk < resources.disk_gib || disk > 65536 {
                        return Err(EngineError::invalid(format!(
                            "Linux template needs disk_gib {}..=65536",
                            resources.disk_gib
                        )));
                    }
                }
                lamp_engine::engine::MachineCfg::Macos {
                    default_resources,
                    minimum_resources,
                    ..
                } => {
                    if disk != default_resources.disk_gib
                        || cpus < minimum_resources.vcpus as u64
                        || memory < minimum_resources.memory_mib
                    {
                        return Err(EngineError::invalid(format!(
                            "template requires at least {} vcpus / {} MiB, exactly {} GiB",
                            minimum_resources.vcpus,
                            minimum_resources.memory_mib,
                            default_resources.disk_gib
                        )));
                    }
                }
            }
            out.state = "checked".into();
        }
        "validate" => {
            let def = definition::parse(&request.definition)?;
            out.detail
                .insert("definition".into(), serde_json::to_string(&def).unwrap());
        }
        "build" => {
            #[cfg(target_os = "linux")]
            let log_key = request.definition.clone();
            #[cfg(target_os = "macos")]
            let log_key = definition::normalized(&definition::parse(&request.definition)?)
                .unwrap_or_else(|| request.definition.clone());
            lamp_engine::buildlog::write(&engine.cfg.store, &log_key, "")?;
            #[cfg(target_os = "linux")]
            let report = lamp_engine::build::build_template(engine, &request.definition)?;
            #[cfg(target_os = "macos")]
            let report = {
                let def = definition::parse(&request.definition)?;
                let seed = definition::parse_seed_ref(&def.seed_ref)?;
                let dir = engine.cfg.store.join("seeds").join(seed.digest);
                engine.build_linux_definition(&dir, &def)?
            };
            #[cfg(target_os = "macos")]
            out.detail.insert("fixed_resources".into(), "false".into());
            out.state = "built".into();
            let t = engine.template(&report.template_id)?;
            out.detail.insert("arch".into(), t.manifest.arch.clone());
            out.detail
                .insert("guest_os".into(), t.manifest.guest_os.clone());
            #[cfg(target_os = "linux")]
            let resources = &t.variant.resources;
            #[cfg(target_os = "macos")]
            let resources = match &t.machine {
                lamp_engine::engine::MachineCfg::Linux { resources, .. } => resources,
                lamp_engine::engine::MachineCfg::Macos {
                    default_resources, ..
                } => default_resources,
            };
            out.detail.insert(
                "resources".into(),
                serde_json::to_string(resources).unwrap(),
            );
            out.handles.push(report.template_id);
            out.detail
                .insert("log".into(), lamp_engine::buildlog::tail(&report.log));
            out.detail
                .insert("manifest_digest".into(), report.manifest_digest);
        }
        _ => return Err(EngineError::invalid("unknown template action")),
    }
    Ok(out)
}
