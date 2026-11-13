use std::process::Command;

#[derive(Debug, serde::Serialize)]
pub struct Memory {
    pub physical_mib: u64,
    pub available_mib: u64,
    pub host_reserve_mib: u64,
    pub available_reserve_mib: u64,
}

impl Memory {
    pub fn detect() -> Result<Self, String> {
        let physical = Command::new("/usr/sbin/sysctl")
            .args(["-n", "hw.memsize"])
            .output()
            .map_err(|e| e.to_string())?;
        let physical_mib = String::from_utf8_lossy(&physical.stdout)
            .trim()
            .parse::<u64>()
            .map_err(|_| "cannot read physical host memory")?
            >> 20;
        let stats = Command::new("/usr/bin/vm_stat")
            .output()
            .map_err(|e| e.to_string())?;
        if !physical.status.success() || !stats.status.success() {
            return Err("cannot observe host memory".into());
        }
        let stats = String::from_utf8_lossy(&stats.stdout);
        let page = stats
            .lines()
            .next()
            .and_then(|s| s.split("page size of ").nth(1))
            .and_then(|s| s.split_whitespace().next())
            .and_then(|s| s.parse::<u64>().ok())
            .ok_or("cannot read host page size")?;
        let mut available = 0;
        for key in ["Pages free:", "Pages inactive:", "Pages speculative:"] {
            available += stats
                .lines()
                .find_map(|s| s.strip_prefix(key))
                .and_then(|s| s.trim().trim_end_matches('.').parse::<u64>().ok())
                .ok_or("cannot read available host pages")?;
        }
        let available_mib = available.saturating_mul(page) >> 20;
        let host_reserve_mib = (physical_mib / 4).min(2048);
        // Already-running host apps are outside the available-page pool.
        // Preserve the physical allocation cap, while leaving additional
        // headroom within that pool proportional to what remains.
        let available_reserve_mib = (available_mib / 4).max(256).min(host_reserve_mib);
        Ok(Self {
            physical_mib,
            available_mib,
            host_reserve_mib,
            available_reserve_mib,
        })
    }

    pub fn admit(
        &self,
        requested_mib: u64,
        reserved_mib: u64,
        pending_mib: u64,
    ) -> Result<(), String> {
        let charge = requested_mib
            .checked_add(256)
            .ok_or("NO_CAPACITY: memory reservation overflows")?;
        if charge
            > self
                .physical_mib
                .saturating_sub(self.host_reserve_mib)
                .saturating_sub(reserved_mib)
            || charge
                > self
                    .available_mib
                    .saturating_sub(self.available_reserve_mib)
                    .saturating_sub(pending_mib)
        {
            return Err(format!(
                "NO_CAPACITY: requested {requested_mib} MiB plus 256 MiB VMM allowance; physical={} MiB, available={} MiB, host reserve={} MiB, available headroom={} MiB, reservations={reserved_mib} MiB, pending={pending_mib} MiB; retry when memory is available or use a smaller guest or another host",
                self.physical_mib, self.available_mib, self.host_reserve_mib, self.available_reserve_mib
            ));
        }
        Ok(())
    }
}
