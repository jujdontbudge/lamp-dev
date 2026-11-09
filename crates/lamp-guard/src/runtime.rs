use crate::{
    Config, Tuple,
    audit::{Spool, now_ms},
    netlink::Queue,
    packet, public_v4,
};
use serde_json::json;
use std::{
    collections::HashMap,
    ffi::CString,
    io::{self, Write},
    process::{Command, Stdio},
    time::{Duration, Instant},
};
const MAX_FLOWS: usize = 4096;
const IDLE: Duration = Duration::from_secs(30);
struct Flow {
    id: u64,
    start: u128,
    last: Instant,
    sent: u64,
    received: u64,
}

pub fn run(config: Config) -> io::Result<()> {
    config.validate()?;
    if std::fs::read_link("/proc/self/ns/net")? == std::fs::read_link("/proc/1/ns/net")? {
        return Err(io::Error::other(
            "guard requires a supervisor-owned private network namespace",
        ));
    }
    let name = CString::new(config.interface.as_str()).unwrap();
    let interface = unsafe { libc::if_nametoindex(name.as_ptr()) };
    if interface == 0 {
        return Err(io::Error::other("assigned interface does not exist"));
    }
    let mut spool = Spool::create(
        &config.spool,
        config.spool_bytes,
        config.attribution.clone(),
    )?;
    let mut queue = Queue::bind(config.queue)?;
    // Atomic nft transaction. An existing table causes admission failure, never a flush.
    let mut child = Command::new("nft")
        .args(["-f", "-"])
        .stdin(Stdio::piped())
        .spawn()?;
    child
        .stdin
        .take()
        .unwrap()
        .write_all(config.nft()?.as_bytes())?;
    if !child.wait()?.success() {
        return Err(io::Error::other("nft policy install failed"));
    }
    use std::os::fd::AsRawFd;
    crate::confinement::enter(
        config.guard_uid,
        true,
        &[spool.as_raw_fd(), queue.as_raw_fd()],
    )?;
    spool.record(
        "host_guard",
        json!({"type":"lifecycle","state":"ready","filter_mode":"transport",
        "ipv6":"deny","max_flows":MAX_FLOWS,"idle_deadline_ms":IDLE.as_millis()}),
    )?;
    println!(
        "{}",
        json!({"state":"ready","policy_revision":config.attribution.policy_revision})
    );
    let mut flows: HashMap<Tuple, Flow> = HashMap::new();
    let mut next = 0_u64;
    let mut reaped = Instant::now();
    loop {
        for p in queue.packets()? {
            if reaped.elapsed() >= Duration::from_secs(1) {
                for (tuple, flow) in &flows {
                    if flow.last.elapsed() >= IDLE {
                        spool.record("host_guard",json!({"type":"connection","visibility":"opaque","phase":"end",
                            "flow_id":flow.id,"destination":tuple,"started_at_ms":flow.start,"ended_at_ms":now_ms(),
                            "bytes_sent":flow.sent,"bytes_received":flow.received,"reason":"idle_deadline"}))?;
                    }
                }
                flows.retain(|_, f| f.last.elapsed() < IDLE);
                reaped = Instant::now();
            }
            let tuple = packet(&p.bytes);
            let outbound = p.input == interface && p.output != interface;
            let inbound = p.output == interface && p.input != interface;
            let mut allow = false;
            let mut flow_id = None;
            let mut counters = None;
            let mut reason = "unsupported_packet";
            if let Some(t) = &tuple {
                reason = "unsolicited_or_spoofed";
                if p.hook == 2 && outbound && t.source == config.guest_ip {
                    reason = "private_lan_metadata_or_host";
                    if public_v4(t.destination, &config.host_denies) {
                        reason = "flow_limit_or_non_syn";
                        // UDP datagrams and TCP SYN (not SYN+ACK) establish an outbound flow.
                        let open = t.protocol == 17 || p.bytes[33] & 0x12 == 0x02;
                        if !flows.contains_key(t) && open && flows.len() < MAX_FLOWS {
                            next = next
                                .checked_add(1)
                                .ok_or_else(|| io::Error::other("flow identity exhausted"))?;
                            flows.insert(
                                t.clone(),
                                Flow {
                                    id: next,
                                    start: now_ms(),
                                    last: Instant::now(),
                                    sent: 0,
                                    received: 0,
                                },
                            );
                        }
                        if let Some(f) = flows.get_mut(t) {
                            f.sent += p.bytes.len() as u64;
                            f.last = Instant::now();
                            allow = true;
                            flow_id = Some(f.id);
                            counters = Some((f.sent, f.received, f.start));
                            reason = "public_transport";
                        }
                    }
                } else if p.hook == 2 && inbound && t.destination == config.guest_ip {
                    if let Some(f) = flows.get_mut(&t.reverse()) {
                        f.received += p.bytes.len() as u64;
                        f.last = Instant::now();
                        allow = true;
                        flow_id = Some(f.id);
                        counters = Some((f.sent, f.received, f.start));
                        reason = "return_flow";
                    }
                } else {
                    reason = "host_input_or_spoofed";
                }
            }
            // Every packet is durably acknowledged *before* a verdict. On failure,
            // dropping the queue socket drops queued and future packets, including established flows.
            spool.record("host_guard",json!({"type":"connection","visibility":"opaque","phase":"packet",
                "decision":if allow {"allow"}else{"deny"},"reason":reason,"tuple":tuple,
                "flow_id":flow_id,"ip_bytes":p.bytes.len(),"counters_sent_received_started_ms":counters,
                "ip_version":p.bytes.first().map(|v|v>>4)}))?;
            queue.verdict(p.id, allow)?;
        }
    }
}
