//! Read-only guest kernel observations; no task or shell is started.
use std::collections::BTreeMap;

unsafe extern "C" {
    fn mach_host_self() -> libc::host_t;
    fn mach_port_deallocate(
        task: libc::mach_port_t,
        name: libc::mach_port_t,
    ) -> libc::kern_return_t;
    static mach_task_self_: libc::mach_port_t;
}

pub fn collect() -> BTreeMap<String, u64> {
    let mut out = BTreeMap::new();
    // SAFETY: every kernel API receives an initialized buffer and its size.
    unsafe {
        let host = mach_host_self();
        let mut cpu: libc::host_cpu_load_info = std::mem::zeroed();
        let mut count = libc::HOST_CPU_LOAD_INFO_COUNT;
        if libc::host_statistics(
            host,
            libc::HOST_CPU_LOAD_INFO,
            (&raw mut cpu).cast(),
            &mut count,
        ) == 0
        {
            let ticks = u64::from(cpu.cpu_ticks[0])
                + u64::from(cpu.cpu_ticks[1])
                + u64::from(cpu.cpu_ticks[3]);
            let hz = libc::sysconf(libc::_SC_CLK_TCK);
            if hz > 0 {
                out.insert("cpu_usage_usec".into(), ticks * 1_000_000 / hz as u64);
            }
        }
        let mut total = 0u64;
        let mut size = std::mem::size_of_val(&total);
        let mut vm: libc::vm_statistics64 = std::mem::zeroed();
        let mut count = libc::HOST_VM_INFO64_COUNT;
        if libc::sysctlbyname(
            c"hw.memsize".as_ptr(),
            (&raw mut total).cast(),
            &mut size,
            std::ptr::null_mut(),
            0,
        ) == 0
            && libc::host_statistics64(host, libc::HOST_VM_INFO64, (&raw mut vm).cast(), &mut count)
                == 0
        {
            let pages = u64::from(vm.free_count) + u64::from(vm.inactive_count);
            let page_size = libc::sysconf(libc::_SC_PAGESIZE);
            if page_size > 0 {
                out.insert("memory_total_bytes".into(), total);
                out.insert(
                    "memory_used_bytes".into(),
                    total.saturating_sub(pages * page_size as u64),
                );
            }
        }
        // Release the send right returned by mach_host_self on every sample.
        mach_port_deallocate(mach_task_self_, host);
        let mut disk: libc::statfs = std::mem::zeroed();
        if libc::statfs(c"/".as_ptr(), &mut disk) == 0 {
            out.insert(
                "disk_used_bytes".into(),
                (disk.f_blocks - disk.f_bfree) * u64::from(disk.f_bsize),
            );
            out.insert(
                "disk_total_bytes".into(),
                disk.f_blocks * u64::from(disk.f_bsize),
            );
        }
    }
    // netstat reads the kernel's interface counters. Link rows avoid counting
    // each address on an interface again; loopback is not sandbox network I/O.
    if let Ok(result) = std::process::Command::new("/usr/sbin/netstat")
        .arg("-ibn")
        .output()
        && result.status.success()
    {
        let text = String::from_utf8_lossy(&result.stdout);
        let mut lines = text.lines();
        let header: Vec<_> = lines.next().unwrap_or("").split_whitespace().collect();
        if let (Some(rx_col), Some(tx_col)) = (
            header.iter().position(|v| *v == "Ibytes"),
            header.iter().position(|v| *v == "Obytes"),
        ) {
            let (mut rx, mut tx) = (0, 0);
            for line in lines {
                let fields: Vec<_> = line.split_whitespace().collect();
                if fields.first().is_some_and(|name| name.starts_with("lo"))
                    || !fields.iter().any(|v| v.starts_with("<Link#"))
                {
                    continue;
                }
                rx += fields
                    .get(rx_col)
                    .and_then(|v| v.parse::<u64>().ok())
                    .unwrap_or(0);
                tx += fields
                    .get(tx_col)
                    .and_then(|v| v.parse::<u64>().ok())
                    .unwrap_or(0);
            }
            out.insert("network_rx_bytes".into(), rx);
            out.insert("network_tx_bytes".into(), tx);
        }
    }
    out
}
