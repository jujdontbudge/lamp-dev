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
    fn live(&self) -> io::Result<()> {
        if self.last_audit.elapsed() > AUDIT_DEADLINE {
            return Err(io::Error::other("audit liveness deadline exceeded"));
        }
        Ok(())
    }
    fn durable(&mut self, spool: &mut Spool, events: Vec<serde_json::Value>) -> io::Result<()> {
        self.live()?;
        spool.record_batch("host_guard", events)?;
        // A late successful barrier cannot rescue a missed audit deadline.
        if self.last_audit.elapsed() > AUDIT_DEADLINE {
            return Err(io::Error::other("audit storage deadline exceeded"));
        }
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
        self.live()?;
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
        self.live()?;
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
        "audit_batch_ms":AUDIT_BATCH.as_millis(),"exceptions":config.exceptions,"proxy":config.proxy,"audit_deadline_ms":AUDIT_DEADLINE.as_millis(),"max_pending_denials":MAX_DENIALS}))?;
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

#[cfg(test)]
mod tests {
    use super::*;
    fn fixture(name: &str) -> (Config, Spool, Connections) {
        let path =
            std::env::temp_dir().join(format!("lamp-connections-{}-{name}", std::process::id()));
        let config: Config = serde_json::from_value(json!({
            "attribution":{"principal_id":"p","host_id":"h","task_id":"t","sandbox_id":"s",
                "generation":1,"session_id":"s","operation_id":"o","policy_revision":1},
            "interface":"task0","guest_ip":"198.18.240.2","queue":314,"guard_uid":1000001,
            "host_denies":["198.18.240.1/32"],"spool":path,"spool_bytes":1048576}))
        .unwrap();
        let spool = Spool::create(
            &config.spool,
            config.spool_bytes,
            config.attribution.clone(),
        )
        .unwrap();
        (config, spool, Connections::new())
    }
    fn tcp(flags: u8, seq: u32, data: usize) -> crate::netlink::Packet {
        let mut bytes = vec![0; 40 + data];
        let len = bytes.len() as u16;
        bytes[0] = 0x45;
        bytes[2..4].copy_from_slice(&len.to_be_bytes());
        bytes[9] = 6;
        bytes[12..16].copy_from_slice(&[198, 18, 240, 2]);
        bytes[16..20].copy_from_slice(&[8, 8, 8, 8]);
        bytes[20..22].copy_from_slice(&12345_u16.to_be_bytes());
        bytes[22..24].copy_from_slice(&18080_u16.to_be_bytes());
        bytes[24..28].copy_from_slice(&seq.to_be_bytes());
        bytes[32] = 0x50;
        bytes[33] = flags;
        crate::netlink::Packet {
            id: 1,
            hook: 2,
            input: 1,
            output: 2,
            bytes,
        }
    }
    fn records(c: &Config) -> Vec<serde_json::Value> {
        std::fs::read_to_string(&c.spool)
            .unwrap()
            .lines()
            .map(|s| serde_json::from_str(s).unwrap())
            .collect()
    }
    #[test]
    fn revision_fences_existing_authority_and_preserves_parent_ceiling() {
        let (mut c, mut s, mut state) = fixture("revision-fence");
        c.policy_ceiling = serde_json::from_value(json!([{
            "filter_mode":"transport", "default_egress":"deny",
            "allow":[{"cidr":"8.8.8.8/32","protocol":"tcp","ports":[18080]}]
        }]))
        .unwrap();
        c.require_allowlist = true;
        c.validate().unwrap();
        assert!(state.packet(&c, 1, &mut s, &tcp(2, 1, 0)).unwrap());
        let update: Update = serde_json::from_value(json!({"expected_revision":1,"revision":2,
            "policy_layers":[{"filter_mode":"transport","default_egress":"deny"}]}))
        .unwrap();
        update.validate(1, c.revision_requires_allowlist()).unwrap();
        state.apply(&mut c, &mut s, update).unwrap();
        assert_eq!(c.attribution.policy_revision, 2);
        assert!(state.flows.is_empty());
        assert!(!state.packet(&c, 1, &mut s, &tcp(0x18, 2, 100)).unwrap());
        assert!(!state.packet(&c, 1, &mut s, &tcp(2, 3, 0)).unwrap());
        let update = Update {
            expected_revision: 2,
            revision: 3,
            policy_layers: vec![],
        };
        update.validate(2, c.revision_requires_allowlist()).unwrap();
        state.apply(&mut c, &mut s, update).unwrap();
        // ACK/data on an old tuple cannot borrow the previous TCP admission.
        assert!(!state.packet(&c, 1, &mut s, &tcp(0x18, 2, 100)).unwrap());
        assert!(state.packet(&c, 1, &mut s, &tcp(2, 4, 0)).unwrap());
        let mut forbidden = tcp(2, 5, 0);
        forbidden.bytes[16..20].copy_from_slice(&[1, 1, 1, 1]);
        assert!(!state.packet(&c, 1, &mut s, &forbidden).unwrap());
        let rows = records(&c);
        assert!(
            rows.iter()
                .any(|r| r["details"]["reason"] == "policy_revision_fence"
                    && r["attribution"]["policy_revision"] == 1)
        );
        assert!(
            rows.iter()
                .any(|r| r["details"]["phase"] == "start"
                    && r["attribution"]["policy_revision"] == 3)
        );
        std::fs::remove_file(c.spool).unwrap();
    }
    #[test]
    fn failed_revision_barrier_never_changes_active_policy() {
        let (mut c, mut s, mut state) = fixture("revision-spool-failure");
        while s
            .record("test", json!({"padding":"x".repeat(2048)}))
            .is_ok()
        {}
        while s.record("test", json!({"type":"heartbeat"})).is_ok() {}
        let update = Update {
            expected_revision: 1,
            revision: 2,
            policy_layers: vec![],
        };
        assert!(state.apply(&mut c, &mut s, update).is_err());
        assert_eq!(c.attribution.policy_revision, 1);
        std::fs::remove_file(c.spool).unwrap();
    }
    #[test]
    fn established_flow_adds_no_per_packet_durable_writes() {
        let (c, mut s, mut state) = fixture("established");
        assert!(state.packet(&c, 1, &mut s, &tcp(2, 100, 0)).unwrap());
        assert_eq!(s.durable_writes, 1);
        for _ in 0..1000 {
            assert!(state.packet(&c, 1, &mut s, &tcp(0x18, 101, 100)).unwrap());
        }
        assert_eq!(s.durable_writes, 1, "ACKs/data cannot add storage barriers");
        state.flush(&mut s, None).unwrap();
        assert_eq!(
            s.durable_writes, 2,
            "all counters and heartbeat use one bounded barrier"
        );
        assert_eq!(
            records(&c).last().unwrap()["details"]["bytes_sent"],
            140040_u64
        );
        state.last_audit = Instant::now() - AUDIT_DEADLINE - Duration::from_millis(1);
        assert!(state.packet(&c, 1, &mut s, &tcp(0x10, 101, 0)).is_err());
        std::fs::remove_file(c.spool).unwrap();
    }
    #[test]
    fn denials_are_batched_and_capacity_fails_closed() {
        let (c, mut s, mut state) = fixture("denials");
        let mut p = tcp(2, 1, 0);
        p.bytes[16] = 10;
        for _ in 0..1000 {
            assert!(!state.packet(&c, 1, &mut s, &p).unwrap());
        }
        assert_eq!(s.durable_writes, 0);
        state.flush(&mut s, None).unwrap();
        assert_eq!(s.durable_writes, 1);
        assert_eq!(records(&c).last().unwrap()["details"]["packets"], 1000);
        for port in 0..MAX_DENIALS {
            p.bytes[20..22].copy_from_slice(&(port as u16).to_be_bytes());
            assert!(!state.packet(&c, 1, &mut s, &p).unwrap());
        }
        p.bytes[20..22].copy_from_slice(&(MAX_DENIALS as u16).to_be_bytes());
        assert!(
            state
                .packet(&c, 1, &mut s, &p)
                .unwrap_err()
                .to_string()
                .contains("capacity")
        );
        assert_eq!(
            s.durable_writes, 2,
            "overflow emits one durable fault then stops, not a sync storm"
        );
        std::fs::remove_file(c.spool).unwrap();
    }
    #[test]
    fn tcp_rst_then_reconnect_has_two_admissions() {
        let (c, mut s, mut state) = fixture("rst");
        for p in [
            tcp(2, 100, 0),
            tcp(0x18, 101, 100),
            tcp(4, 201, 0),
            tcp(2, 500, 0),
        ] {
            assert!(state.packet(&c, 1, &mut s, &p).unwrap());
        }
        let rows = records(&c);
        let starts: Vec<_> = rows
            .iter()
            .filter(|r| r["details"]["phase"] == "start")
            .collect();
        assert_eq!(starts.len(), 2);
        assert_ne!(
            starts[0]["details"]["flow_id"],
            starts[1]["details"]["flow_id"]
        );
        assert_eq!(starts[1]["details"]["bytes_sent"], 40);
        assert!(
            rows.iter()
                .any(|r| r["details"]["reason"] == "tcp_rst" && r["details"]["bytes_sent"] == 220)
        );
        std::fs::remove_file(c.spool).unwrap();
    }
    #[test]
    fn fin_drains_and_same_isn_retransmission_does_not_readmit() {
        let (c, mut s, mut state) = fixture("fin");
        let syn = tcp(2, 100, 0);
        for _ in 0..3 {
            assert!(state.packet(&c, 1, &mut s, &syn).unwrap());
        }
        assert_eq!(s.durable_writes, 1);
        assert!(state.packet(&c, 1, &mut s, &tcp(0x11, 101, 0)).unwrap());
        assert_eq!(s.durable_writes, 2);
        assert!(state.packet(&c, 1, &mut s, &syn).unwrap());
        assert_eq!(s.durable_writes, 4);
        assert!(state.packet(&c, 1, &mut s, &syn).unwrap());
        assert_eq!(s.durable_writes, 4);
        assert_eq!(state.flows.values().next().unwrap().id, 2);
        assert!(state.packet(&c, 1, &mut s, &tcp(2, 500, 0)).unwrap());
        assert_eq!(state.flows.values().next().unwrap().id, 3);
        assert_eq!(
            s.durable_writes, 6,
            "a different ISN closes the old record and admits a new one"
        );
        std::fs::remove_file(c.spool).unwrap();
    }
    #[test]
    fn fin_then_peer_data_crash_remains_unterminated() {
        let (c, mut s, mut state) = fixture("fin-crash");
        assert!(state.packet(&c, 1, &mut s, &tcp(2, 100, 0)).unwrap());
        assert!(state.packet(&c, 1, &mut s, &tcp(0x11, 101, 0)).unwrap());
        let mut peer = tcp(0x18, 500, 100);
        peer.input = 2;
        peer.output = 1;
        peer.bytes[12..16].copy_from_slice(&[8, 8, 8, 8]);
        peer.bytes[16..20].copy_from_slice(&[198, 18, 240, 2]);
        peer.bytes[20..22].copy_from_slice(&18080_u16.to_be_bytes());
        peer.bytes[22..24].copy_from_slice(&12345_u16.to_be_bytes());
        assert!(state.packet(&c, 1, &mut s, &peer).unwrap());
        state.flush(&mut s, None).unwrap();
        let durable = s.durable_writes;
        assert!(state.packet(&c, 1, &mut s, &peer).unwrap());
        assert_eq!(s.durable_writes, durable);
        // Crash before the next batch loses the additional in-memory counters,
        // but neither the FIN record nor a draining checkpoint may claim an end.
        drop(state);
        drop(s);
        let rows = records(&c);
        let connections: Vec<_> = rows
            .iter()
            .map(|r| &r["details"])
            .filter(|r| r["type"] == "connection")
            .collect();
        assert!(connections.iter().any(|r| r["reason"] == "tcp_fin"));
        assert_eq!(connections.last().unwrap()["bytes_received"], 140);
        for r in connections {
            assert_ne!(r["phase"], "end");
            assert_eq!(r["closed"], false);
            assert!(r["ended_at_ms"].is_null());
        }
        std::fs::remove_file(c.spool).unwrap();
    }
    #[test]
    fn idle_tick_expires_and_audit_failure_stops_established_traffic() {
        let (c, mut s, mut state) = fixture("idle-failure");
        assert!(state.packet(&c, 1, &mut s, &tcp(2, 1, 0)).unwrap());
        state.flows.values_mut().next().unwrap().last = Instant::now() - IDLE;
        state.last_batch = Instant::now() - AUDIT_BATCH;
        state.tick(&mut s).unwrap();
        assert!(state.flows.is_empty());
        assert!(
            records(&c)
                .iter()
                .any(|r| r["details"]["reason"] == "idle_deadline")
        );
        assert!(state.packet(&c, 1, &mut s, &tcp(2, 500, 0)).unwrap());
        assert!(state.packet(&c, 1, &mut s, &tcp(0x18, 501, 100)).unwrap());
        while s
            .record("test", json!({"padding":"x".repeat(60*1024)}))
            .is_ok()
        {}
        while s.record("test", json!({"type":"heartbeat"})).is_ok() {}
        state.last_audit = Instant::now();
        state.last_batch = Instant::now() - AUDIT_BATCH;
        assert!(
            state
                .tick(&mut s)
                .unwrap_err()
                .to_string()
                .contains("spool full")
        );
        // The run loop propagates this error without issuing another verdict.
        std::fs::remove_file(c.spool).unwrap();
    }
}
