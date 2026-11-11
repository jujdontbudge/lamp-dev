use crate::{
    Config, Tuple,
    audit::{Spool, now_ms},
    control::{Control, Message, Update},
    netlink::Queue,
    packet, policy,
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
const MAX_DENIALS: usize = 128;
const IDLE: Duration = Duration::from_secs(30);
const AUDIT_BATCH: Duration = Duration::from_millis(250);
const AUDIT_DEADLINE: Duration = Duration::from_secs(1);
struct Flow {
    id: u64,
    start: u128,
    last: Instant,
    last_ms: u128,
    sent: u64,
    received: u64,
    syn: Option<u32>,
    draining: bool,
    dirty: bool,
}
impl Flow {
    fn record(&self, tuple: &Tuple, phase: &str, reason: &str) -> serde_json::Value {
        json!({"type":"connection","visibility":"opaque","phase":phase,"decision":"allow",
            "flow_id":self.id,"tuple":tuple,"reason":reason,"started_at_ms":self.start,
            "ended_at_ms":if phase=="end" {Some(self.last_ms)}else{None},
            "bytes_sent":self.sent,"bytes_received":self.received,"closed":phase=="end"})
    }
}
#[derive(Hash, PartialEq, Eq)]
struct DenialKey {
    tuple: Option<Tuple>,
    reason: &'static str,
    version: u8,
}
struct Denial {
    packets: u64,
    bytes: u64,
    first: u128,
    last: u128,
}
struct Connections {
    flows: HashMap<Tuple, Flow>,
    denials: HashMap<DenialKey, Denial>,
    next: u64,
    last_audit: Instant,
    last_batch: Instant,
}
impl Connections {
    fn new() -> Self {
        Self {
            flows: HashMap::new(),
            denials: HashMap::new(),
            next: 0,
            last_audit: Instant::now(),
            last_batch: Instant::now(),
        }
    }
    fn live(&mut self, spool: &mut Spool) -> io::Result<()> {
        if self.last_audit.elapsed() > AUDIT_DEADLINE {
            self.flush(spool, None)?;
        }
        Ok(())
    }
    fn durable(&mut self, spool: &mut Spool, events: Vec<serde_json::Value>) -> io::Result<()> {
        // This is the sole verdict thread: a blocked storage barrier blocks
        // both directions of every flow, including established connections.
        // A slow successful fsync is not lost audit. Resume only after it is
        // durable, rather than permanently killing networking for a disk pause.
        // Actual I/O failure still propagates and closes the entire path.
        spool.record_batch("host_guard", events)?;
        self.last_audit = Instant::now();
        Ok(())
    }
    fn flush(&mut self, spool: &mut Spool, fault: Option<serde_json::Value>) -> io::Result<()> {
        let now = Instant::now();
        let mut events = vec![json!({"type":"heartbeat","flow_count":self.flows.len()})];
        for (tuple, f) in &self.flows {
            let expired = now.duration_since(f.last) >= IDLE;
            if f.dirty || expired {
                events.push(f.record(
                    tuple,
                    if expired { "end" } else { "checkpoint" },
                    if expired {
                        "idle_deadline"
                    } else {
                        "counter_batch"
                    },
                ));
            }
        }
        for (key, d) in &self.denials {
            events.push(json!({"type":"connection","visibility":"opaque","phase":"deny","decision":"deny",
                "tuple":key.tuple,"reason":key.reason,"ip_version":key.version,
                "packets":d.packets,"ip_bytes":d.bytes,"started_at_ms":d.first,"ended_at_ms":d.last}));
        }
        events.extend(fault);
        self.durable(spool, events)?;
        self.flows.retain(|_, f| now.duration_since(f.last) < IDLE);
        for f in self.flows.values_mut() {
            f.dirty = false;
        }
        self.denials.clear();
        self.last_batch = Instant::now();
        Ok(())
    }
    fn tick(&mut self, spool: &mut Spool) -> io::Result<()> {
        self.live(spool)?;
        if self.last_batch.elapsed() >= AUDIT_BATCH {
            self.flush(spool, None)?;
        }
        Ok(())
    }
    fn deny(
        &mut self,
        spool: &mut Spool,
        tuple: Option<Tuple>,
        reason: &'static str,
        bytes: &[u8],
    ) -> io::Result<bool> {
        let key = DenialKey {
            tuple,
            reason,
            version: bytes.first().map_or(0, |v| v >> 4),
        };
        if !self.denials.contains_key(&key) && self.denials.len() == MAX_DENIALS {
            // A flood cannot force per-denial fsyncs or silently discard identities.
            // Persist the pending batch and fault once, then close the entire path.
            self.flush(
                spool,
                Some(json!({"type":"audit.gap","decision":"deny",
                "reason":"denial_capacity","tuple":key.tuple,"ip_version":key.version})),
            )?;
            return Err(io::Error::other("denial audit capacity exhausted"));
        }
        let now = now_ms();
        let d = self.denials.entry(key).or_insert(Denial {
            packets: 0,
            bytes: 0,
            first: now,
            last: now,
        });
        d.packets += 1;
        d.bytes += bytes.len() as u64;
        d.last = now;
        Ok(false)
    }
    /// Fence all old flow identities before acknowledging a revision. This
    /// deliberately also retires still-allowed TCP flows: they must reconnect
    /// with a fresh SYN rather than inherit an admission from an older policy.
    fn apply(&mut self, config: &mut Config, spool: &mut Spool, update: Update) -> io::Result<()> {
        let mut next = config.clone();
        next.policy_layers = update.policy_layers;
        next.attribution.policy_revision = update.revision;
        let digest = next.policy_digest()?;
        self.flush(spool, None)?;
        let mut events: Vec<_> = self
            .flows
            .iter()
            .map(|(t, f)| f.record(t, "end", "policy_revision_fence"))
            .collect();
        events.push(
            json!({"type":"policy_change","previous_revision":config.attribution.policy_revision,
            "effective_policy_revision":update.revision,"policy_sha256":digest,"flow_action":"retire_all"}),
        );
        self.durable(spool, events)?;
        self.flows.clear();
        spool.set_policy_revision(update.revision)?;
        *config = next;
        Ok(())
    }
    fn control(
        &mut self,
        control: &mut Control,
        config: &mut Config,
        spool: &mut Spool,
    ) -> io::Result<()> {
        if let Some(message) = control.receive()? {
            let error = match message {
                Message::Invalid => Some("INVALID_POLICY"),
                Message::Update(update) => match update.validate(
                    config.attribution.policy_revision,
                    config.revision_requires_allowlist(),
                ) {
                    Err(error) => Some(error),
                    Ok(()) => {
                        self.apply(config, spool, update)?;
                        None
                    }
                },
            };
            // If the supervisor cannot observe this ACK, stop instead of
            // admitting traffic under an uncertain revision.
            control.reply(config.attribution.policy_revision, error)?;
        }
        Ok(())
    }
    fn packet(
        &mut self,
        config: &Config,
        interface: u32,
        spool: &mut Spool,
        p: &crate::netlink::Packet,
    ) -> io::Result<bool> {
        self.live(spool)?;
        let Some(t) = packet(&p.bytes) else {
            return self.deny(spool, None, "unsupported_packet", &p.bytes);
        };
        let outbound = p.input == interface && p.output != interface;
        let inbound = p.output == interface && p.input != interface;
        let key;
        let mut admitted = false;
        // Hooks: 1 LOCAL_IN, 2 FORWARD, 3 LOCAL_OUT. Local traffic exists
        // only for the mediated proxy endpoint, in both directions.
        let forward = p.hook == 2;
        let proxied_in = p.hook == 1 && config.to_proxy(&t);
        let proxied_out = p.hook == 3 && config.to_proxy(&t.reverse());
        if (forward || proxied_in) && outbound && t.source == config.guest_ip {
            if config.subscription_domains.is_some() && !proxied_in {
                return self.deny(spool, Some(t), "subscription_proxy_only", &p.bytes);
            }
            let reason = if proxied_in {
                "mediated_proxy"
            } else {
                match policy::decision(config, &t) {
                    Ok(reason) => reason,
                    Err(reason) => return self.deny(spool, Some(t), reason, &p.bytes),
                }
            };
            let syn = if t.protocol == 6 && p.bytes[33] & 0x17 == 0x02 {
                Some(u32::from_be_bytes(p.bytes[24..28].try_into().unwrap()))
            } else {
                None
            };
            // Same ISN on an open tuple is a SYN retransmission, not a new admission.
            // A draining tuple or a different ISN must never inherit the old identity.
            if syn.is_some()
                && self
                    .flows
                    .get(&t)
                    .is_some_and(|f| f.draining || f.syn != syn)
            {
                let old = self.flows.remove(&t).unwrap();
                self.durable(spool, vec![old.record(&t, "end", "replaced_syn")])?;
            }
            if !self.flows.contains_key(&t) {
                if (t.protocol != 17 && syn.is_none()) || self.flows.len() == MAX_FLOWS {
                    return self.deny(spool, Some(t), "flow_limit_or_non_syn", &p.bytes);
                }
                self.next = self
                    .next
                    .checked_add(1)
                    .ok_or_else(|| io::Error::other("flow identity exhausted"))?;
                let now = now_ms();
                let f = Flow {
                    id: self.next,
                    start: now,
                    last: Instant::now(),
                    last_ms: now,
                    sent: p.bytes.len() as u64,
                    received: 0,
                    syn,
                    draining: false,
                    dirty: false,
                };
                self.durable(spool, vec![f.record(&t, "start", reason)])?;
                self.flows.insert(t.clone(), f);
                admitted = true;
            }
            key = t;
        } else if (forward || proxied_out) && inbound && t.destination == config.guest_ip {
            key = t.reverse();
            if !self.flows.contains_key(&key) {
                return self.deny(spool, Some(t), "unsolicited_or_spoofed", &p.bytes);
            }
        } else {
            return self.deny(spool, Some(t), "host_input_or_spoofed", &p.bytes);
        }
        let f = self.flows.get_mut(&key).unwrap();
        if !admitted {
            if outbound {
                f.sent += p.bytes.len() as u64;
            } else {
                f.received += p.bytes.len() as u64;
            }
            f.dirty = true;
        }
        f.last = Instant::now();
        f.last_ms = now_ms();
        let rst = key.protocol == 6 && p.bytes[33] & 0x04 != 0;
        let fin = key.protocol == 6 && p.bytes[33] & 0x01 != 0;
        if rst || (fin && !f.draining) {
            f.draining = true;
            f.dirty = false;
            let event = f.record(
                &key,
                if rst { "end" } else { "checkpoint" },
                if rst { "tcp_rst" } else { "tcp_fin" },
            );
            self.durable(spool, vec![event])?;
            if rst {
                self.flows.remove(&key);
            }
            // FIN retains a bounded drain entry for half-close data/final ACKs.
            // It remains unterminated until RST, expiry or SYN replacement retires it.
        }
        Ok(true)
    }
}

pub fn run(config: Config) -> io::Result<()> {
    run_with_control(config, None)
}

pub fn run_with_control(mut config: Config, mut control: Option<Control>) -> io::Result<()> {
    config.validate()?;
    if control.is_some() && config.proxy.is_some() {
        // The old single-request proxy has no active-stream revision fence.
        // Refuse partial updates rather than leave its upstream dials stale.
        return Err(io::Error::other(
            "live revisions require transport-only guard; proxy must be fenced and recreated",
        ));
    }
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
    let mut keep = vec![spool.as_raw_fd(), queue.as_raw_fd()];
    if let Some(control) = &control {
        keep.push(control.as_raw_fd());
    }
    crate::confinement::enter(config.guard_uid, true, &keep)?;
    spool.record("host_guard",json!({"type":"lifecycle","state":"ready","filter_mode":"transport","policy_sha256":config.policy_digest()?,
        "ipv6":"deny","policy_layers":config.policy_layers.len(),"policy_ceiling_layers":config.policy_ceiling.len(),"require_allowlist":config.require_allowlist,"live_revisions":control.is_some(),"max_flows":MAX_FLOWS,"idle_deadline_ms":IDLE.as_millis(),
        "audit_batch_ms":AUDIT_BATCH.as_millis(),"exceptions":config.exceptions,"proxy":config.proxy,"audit_flush_due_ms":AUDIT_DEADLINE.as_millis(),"audit_wait_mode":"block_verdict_until_durable","max_pending_denials":MAX_DENIALS}))?;
    println!(
        "{}",
        json!({"state":"ready","policy_revision":config.attribution.policy_revision})
    );
    let mut connections = Connections::new();
    loop {
        if let Some(control) = &mut control {
            connections.control(control, &mut config, &mut spool)?;
        }
        connections.tick(&mut spool)?;
        for p in queue.packets()? {
            if let Some(control) = &mut control {
                connections.control(control, &mut config, &mut spool)?;
            }
            connections.tick(&mut spool)?;
            let allow = connections.packet(&config, interface, &mut spool, &p)?;
            // No kernel bypass: queue/socket/guard loss still drops established
            // traffic. Dirty counters/denials have a bounded durable batch deadline.
            queue.verdict(p.id, allow)?;
        }
    }
}
