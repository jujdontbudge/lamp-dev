//! lamp-vz: the signed, hypervisor-entitled Mac VMM helper.
//!
//! ```text
//! lamp-vz worker --spec FILE [--restore STATE [--paused]] [--no-confine]
//! lamp-vz restore-image                 latest restore image Apple offers this Mac (no download)
//! lamp-vz load-image IPSW               what a local restore image declares
//! lamp-vz install --ipsw IPSW --dir DIR --cpus N --memory-mib M --disk-gib G
//! lamp-vz new-ids                       fresh generic and Mac machine identifiers
//! lamp-vz profile --spec FILE [--restore STATE]   confinement profile and inventory
//! ```
#[cfg(target_os = "macos")]
fn main() {
    let mut args: Vec<String> = std::env::args().skip(1).collect();
    if args
        .iter()
        .take_while(|arg| arg.as_str() != "--")
        .any(|arg| arg == "--help" || arg == "-h")
        || args.first().is_some_and(|arg| arg == "help")
    {
        for line in include_str!("lamp-vz.rs")
            .lines()
            .take_while(|line| line.starts_with("//!"))
        {
            let text = line.trim_start_matches("//!").trim_start();
            if !text.starts_with("```") {
                println!("{text}");
            }
        }
        return;
    }

    if args.is_empty() {
        die(
            "usage: lamp-vz worker|native-task|restore-image|load-image|install|new-ids|profile|validate",
        );
    }
    let cmd = args.remove(0);
    let flag = |args: &mut Vec<String>, name: &str| -> Option<String> {
        let i = args.iter().position(|a| a == name)?;
        let v = args.get(i + 1).cloned();
        args.drain(i..(i + 2).min(args.len()));
        v
    };
    let has = |args: &mut Vec<String>, name: &str| -> bool {
        match args.iter().position(|a| a == name) {
            Some(i) => {
                args.remove(i);
                true
            }
            None => false,
        }
    };
    match cmd.as_str() {
        "memory" => println!(
            "{}",
            serde_json::to_string(&lamp_vz::admission::Memory::detect().unwrap_or_else(|e| die(e)))
                .unwrap()
        ),
        "worker" => {
            let paused = has(&mut args, "--paused");
            let no_confine = has(&mut args, "--no-confine");
            let spec = flag(&mut args, "--spec").unwrap_or_else(|| die("--spec FILE"));
            let restore = flag(&mut args, "--restore");
            if let Err(e) = lamp_vz::worker::run(lamp_vz::worker::Options {
                spec: spec.into(),
                restore: restore.map(Into::into),
                stay_paused: paused,
                confine: !no_confine,
            }) {
                die(e);
            }
        }
        "native-task" => {
            let helper = flag(&mut args, "--helper")
                .unwrap_or_else(|| die("--helper SIGNED_APP_SANDBOX_HELPER"));
            let task = lamp_vz::native::read_task().unwrap_or_else(|e| die(e));
            let status = lamp_vz::native::launch(std::path::Path::new(&helper), &task)
                .unwrap_or_else(|e| die(e));
            std::process::exit(status);
        }
        "restore-image" => match lamp_vz::macos::latest_supported() {
            Ok(i) => println!("{}", serde_json::to_string(&i).unwrap()),
            Err(e) => die(e),
        },
        "load-image" => {
            let p = args.first().unwrap_or_else(|| die("load-image IPSW"));
            match lamp_vz::macos::load(std::path::Path::new(p)) {
                Ok(i) => println!("{}", serde_json::to_string(&i).unwrap()),
                Err(e) => die(e),
            }
        }
        "install" => install(&mut args, flag),
        "new-ids" => println!(
            "{}",
            serde_json::json!({
                "generic_machine_identifier_b64": lamp_vz::macos::b64_encode(&lamp_vz::vz::new_generic_machine_identifier()),
                "mac_machine_identifier_b64": lamp_vz::macos::b64_encode(&lamp_vz::vz::new_mac_machine_identifier()),
                "limits": lamp_vz::vz::host_limits(),
            })
        ),
        "profile" => {
            let spec = flag(&mut args, "--spec").unwrap_or_else(|| die("--spec FILE"));
            let restore = flag(&mut args, "--restore");
            let text = std::fs::read_to_string(&spec).unwrap_or_else(|e| die(e));
            let spec: lamp_vz::spec::WorkerSpec =
                serde_json::from_str(&text).unwrap_or_else(|e| die(e));
            let g =
                lamp_vz::worker::grants_for(&spec, restore.as_deref().map(std::path::Path::new));
            println!(
                "{}",
                serde_json::json!({"profile": lamp_vz::confine::profile(&g), "inventory": lamp_vz::confine::inventory(&g)})
            );
        }
        "validate" => {
            let path = flag(&mut args, "--spec").unwrap_or_else(|| die("--spec FILE"));
            let spec: lamp_vz::spec::WorkerSpec =
                serde_json::from_slice(&std::fs::read(path).unwrap_or_else(|e| die(e)))
                    .unwrap_or_else(|e| die(e));
            let cfg = lamp_vz::worker::config(&spec).unwrap_or_else(|e| die(e));
            unsafe {
                println!(
                    "{}",
                    serde_json::json!({
                        "valid": true,
                        "graphics": cfg.graphicsDevices().len(),
                        "keyboards": cfg.keyboards().len(),
                        "pointing": cfg.pointingDevices().len(),
                        "audio": cfg.audioDevices().len(),
                        "disks": cfg.storageDevices().len(),
                        "devices": spec.devices,
                        "memory": lamp_vz::admission::Memory::detect().unwrap_or_else(|e| die(e)),
                    })
                );
            }
        }
        other => die(format!("unknown command {other}")),
    }
}

#[cfg(target_os = "macos")]
fn install(args: &mut Vec<String>, flag: impl Fn(&mut Vec<String>, &str) -> Option<String>) {
    use lamp_vz::spec::*;
    let ipsw = flag(args, "--ipsw").unwrap_or_else(|| die("--ipsw IPSW"));
    let dir = std::path::PathBuf::from(flag(args, "--dir").unwrap_or_else(|| die("--dir DIR")));
    let cpus: usize = flag(args, "--cpus")
        .and_then(|v| v.parse().ok())
        .unwrap_or(4);
    let memory_mib: u64 = flag(args, "--memory-mib")
        .and_then(|v| v.parse().ok())
        .unwrap_or(4096);
    let disk_gib: u64 = flag(args, "--disk-gib")
        .and_then(|v| v.parse().ok())
        .unwrap_or(48);
    let image = lamp_vz::macos::load(std::path::Path::new(&ipsw)).unwrap_or_else(|e| die(e));
    eprintln!("restore image: {}", serde_json::to_string(&image).unwrap());
    let hw = image
        .hardware_model_b64
        .clone()
        .unwrap_or_else(|| die("restore image has no supported configuration for this Mac"));
    std::fs::create_dir_all(&dir).unwrap_or_else(|e| die(e));
    let aux = dir.join("aux.img");
    let disk = dir.join("disk.img");
    if disk.exists() || aux.exists() {
        die(format!("{} already holds a machine", dir.display()));
    }
    let f = std::fs::File::create(&disk).unwrap_or_else(|e| die(e));
    f.set_len(disk_gib << 30).unwrap_or_else(|e| die(e));
    drop(f);
    lamp_vz::macos::create_aux(&aux, &lamp_vz::macos::b64_decode(&hw).unwrap())
        .unwrap_or_else(|e| die(e));
    let spec = WorkerSpec {
        devices: Devices {
            display: Some(Display {
                width: 1024,
                height: 768,
            }),
            keyboard: true,
            mouse: true,
            gpu: true,
            ..Default::default()
        },
        sandbox_id: "install".into(),
        machine: Machine::Macos {
            hardware_model_b64: hw,
            machine_identifier_b64: lamp_vz::macos::b64_encode(
                &lamp_vz::vz::new_mac_machine_identifier(),
            ),
            auxiliary_storage: aux,
        },
        cpus,
        memory_mib,
        disk_limit_gib: Some(disk_gib),
        disks: vec![DiskSpec {
            path: disk,
            read_only: false,
            sync: Sync::Full,
        }],
        dir: dir.clone(),
        nat_network: false,
        read_paths: vec![],
        write_paths: vec![],
        write_dirs: vec![],
    };
    std::fs::write(
        dir.join("machine.json"),
        serde_json::to_string_pretty(&spec).unwrap(),
    )
    .unwrap_or_else(|e| die(e));
    std::fs::write(
        dir.join("restore-image.json"),
        serde_json::to_string_pretty(&image).unwrap(),
    )
    .unwrap_or_else(|e| die(e));
    let cfg = lamp_vz::worker::config(&spec).unwrap_or_else(|e| die(e));
    let t = std::time::Instant::now();
    let mut last = -1.0;
    lamp_vz::macos::install(cfg, std::path::Path::new(&ipsw), |f| {
        if f - last >= 0.01 {
            last = f;
            eprintln!(
                "install progress {:.0}% after {} s",
                f * 100.0,
                t.elapsed().as_secs()
            );
        }
    })
    .unwrap_or_else(|e| die(e));
    println!(
        "{}",
        serde_json::json!({"installed": dir, "seconds": t.elapsed().as_secs_f64(), "build": image.build, "version": image.version})
    );
}

fn die(msg: impl std::fmt::Display) -> ! {
    eprintln!("lamp-vz: {msg}");
    std::process::exit(1)
}

#[cfg(not(target_os = "macos"))]
fn main() {
    die("lamp-vz runs only on macOS hosts")
}
