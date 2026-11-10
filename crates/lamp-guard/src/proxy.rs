//! Mediated HTTP/1.1 forward proxy for one sandbox (the E02 secured proxy
//! path). It listens on the guard namespace side of the sandbox's interface
//! ([`Config::proxy`]); the guard admits and records every guest flow to it.
//!
//! Each request is checked against the same policy as direct guest traffic
//! (special-use ceiling, host denies, exact exceptions) and a `request`
//! record is durable in the proxy spool before the origin is dialed. Only
//! absolute-form `http://` targets with a numeric IPv4 authority are
//! accepted: the proxy never resolves names, so the checked address is the
//! one dialed. `CONNECT`, request bodies without `Content-Length` and
//! `Transfer-Encoding` are refused. The query string and header values never
//! enter audit. One connection carries one request; connections are served
//! one at a time with bounded timeouts.
use crate::{Config, Tuple, audit::Spool, policy};
use serde_json::json;
use std::io::{self, Read, Write};
use std::net::{Ipv4Addr, SocketAddrV4, TcpListener, TcpStream};
use std::time::{Duration, Instant};

const HEAD_LIMIT: usize = 16 * 1024;
const BODY_LIMIT: u64 = 16 * 1024 * 1024;
const TIMEOUT: Duration = Duration::from_secs(10);
/// Hop-by-hop and proxy headers never forwarded to the origin.
const DROPPED: &[&str] = &[
    "host",
    "connection",
    "proxy-connection",
    "keep-alive",
    "proxy-authorization",
    "proxy-authenticate",
    "te",
    "trailer",
    "upgrade",
];

#[derive(Debug, PartialEq, Eq)]
pub struct Request {
    pub method: String,
    pub address: Ipv4Addr,
    pub port: u16,
    pub authority: String,
    /// Origin-form target sent upstream.
    pub target: String,
    pub path: String,
    pub query: bool,
    pub headers: Vec<(String, String)>,
    pub body: u64,
}

/// A refusal: HTTP status, reason recorded in audit.
pub type Refusal = (u16, &'static str);

fn token(s: &str) -> bool {
    !s.is_empty()
        && s.bytes()
            .all(|c| c.is_ascii_alphanumeric() || b"!#$%&'*+-.^_`|~".contains(&c))
}

/// Parses a request head (everything before the blank line).
pub fn parse(head: &str) -> Result<Request, Refusal> {
    let mut lines = head.split("\r\n");
    let line = lines.next().unwrap_or_default();
    let mut parts = line.split(' ');
    let (Some(method), Some(uri), Some(version), None) =
        (parts.next(), parts.next(), parts.next(), parts.next())
    else {
        return Err((400, "malformed_request_line"));
    };
    if !token(method) || !method.bytes().all(|c| c.is_ascii_uppercase()) {
        return Err((400, "malformed_method"));
    }
    if version != "HTTP/1.1" && version != "HTTP/1.0" {
        return Err((505, "unsupported_version"));
    }
    if method == "CONNECT" {
        return Err((405, "connect_not_offered"));
    }
    if uri.bytes().any(|c| c <= b' ' || c == 0x7f || c == b'#') {
        return Err((400, "malformed_target"));
    }
    let Some(rest) = uri.strip_prefix("http://") else {
        return Err((400, "absolute_http_target_required"));
    };
    let split = rest.find(['/', '?']).unwrap_or(rest.len());
    let (authority, tail) = rest.split_at(split);
    if authority.contains('@') {
        return Err((400, "userinfo_refused"));
    }
    let (host, port) = match authority.rsplit_once(':') {
        Some((h, p)) => match p.parse::<u16>() {
            Ok(n) if n != 0 && p.bytes().all(|c| c.is_ascii_digit()) => (h, n),
            _ => return Err((400, "malformed_port")),
        },
        None => (authority, 80),
    };
    let Ok(address) = host.parse::<Ipv4Addr>() else {
        return Err((400, "numeric_ipv4_authority_required"));
    };
    let target = if tail.is_empty() {
        "/".to_string()
    } else if tail.starts_with('?') {
        format!("/{tail}")
    } else {
        tail.to_string()
    };
    let (path, query) = match target.split_once('?') {
        Some((p, _)) => (p.to_string(), true),
        None => (target.clone(), false),
    };
    let mut headers = Vec::new();
    let mut body = None;
    let mut connection_tokens = Vec::new();
    for line in lines {
        if line.starts_with([' ', '\t']) {
            return Err((400, "obsolete_line_folding"));
        }
        let Some((name, value)) = line.split_once(':') else {
            return Err((400, "malformed_header"));
        };
        if !token(name) {
            return Err((400, "malformed_header"));
        }
        let value = value.trim_matches([' ', '\t']);
        if value.bytes().any(|c| (c < b' ' && c != b'\t') || c == 0x7f) {
            return Err((400, "malformed_header"));
        }
        let lower = name.to_ascii_lowercase();
        match lower.as_str() {
            "transfer-encoding" => return Err((501, "transfer_encoding_refused")),
            "content-length" => {
                let Ok(n) = value.parse::<u64>() else {
                    return Err((400, "malformed_content_length"));
                };
                if body.is_some_and(|b| b != n) || !value.bytes().all(|c| c.is_ascii_digit()) {
                    return Err((400, "malformed_content_length"));
                }
                if n > BODY_LIMIT {
                    return Err((413, "body_too_large"));
                }
                body = Some(n);
                continue;
            }
            "connection" => connection_tokens.extend(
                value
                    .split(',')
                    .map(|t| t.trim().to_ascii_lowercase())
                    .filter(|t| !t.is_empty()),
            ),
            _ => {}
        }
        headers.push((name.to_string(), value.to_string()));
    }
    headers.retain(|(n, _)| {
        let n = n.to_ascii_lowercase();
        !DROPPED.contains(&n.as_str()) && !connection_tokens.contains(&n)
    });
    Ok(Request {
        method: method.into(),
        address,
        port,
        authority: authority.into(),
        target,
        path,
        query,
        headers,
        body: body.unwrap_or(0),
    })
}

/// Same decision as the guard applies to a direct guest flow.
pub fn allowed(config: &Config, address: Ipv4Addr, port: u16) -> bool {
    if config.subscription_domains.is_some() {
        return false;
    }
    policy::decision(
        config,
        &Tuple {
            source: config.guest_ip,
            destination: address,
            source_port: 0,
            destination_port: port,
            protocol: 6,
        },
    )
    .is_ok()
}

fn refuse(client: &mut TcpStream, status: u16) {
    let reason = match status {
        403 => "Forbidden",
        405 => "Method Not Allowed",
        413 => "Content Too Large",
        501 => "Not Implemented",
        502 => "Bad Gateway",
        505 => "HTTP Version Not Supported",
        _ => "Bad Request",
    };
    let _ = write!(
        client,
        "HTTP/1.1 {status} {reason}\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
    );
}

/// Reads the head; returns it and any body bytes read past it.
fn read_head(client: &mut TcpStream) -> io::Result<Option<(String, Vec<u8>)>> {
    let mut buf = Vec::with_capacity(4096);
    let mut chunk = [0u8; 4096];
    loop {
        let n = client.read(&mut chunk)?;
        if n == 0 {
            return Ok(None);
        }
        buf.extend_from_slice(&chunk[..n]);
        if let Some(end) = buf.windows(4).position(|w| w == b"\r\n\r\n") {
            let rest = buf.split_off(end + 4);
            buf.truncate(end);
            return Ok(String::from_utf8(buf).ok().map(|h| (h, rest)));
        }
        if buf.len() > HEAD_LIMIT {
            return Ok(None);
        }
    }
}

/// Serves one connection: one request, then close. Client I/O failures end
/// only that connection; the only error returned is a spool failure.
fn serve_one(config: &Config, spool: &mut Spool, mut client: TcpStream, id: u64) -> io::Result<()> {
    let _ = client.set_read_timeout(Some(TIMEOUT));
    let _ = client.set_write_timeout(Some(TIMEOUT));
    let started = Instant::now();
    let Some((head, early)) = read_head(&mut client).unwrap_or(None) else {
        spool.record(
            "mediated_proxy",
            json!({"type":"request","request_id":id,"decision":"rejected","reason":"malformed_or_oversized_head"}),
        )?;
        refuse(&mut client, 400);
        return Ok(());
    };
    let request = match parse(&head) {
        Ok(r) => r,
        Err((status, reason)) => {
            spool.record(
                "mediated_proxy",
                json!({"type":"request","request_id":id,"decision":"rejected","reason":reason,"status":status}),
            )?;
            refuse(&mut client, status);
            return Ok(());
        }
    };
    let base = json!({"type":"request","request_id":id,"method":request.method,
        "destination":request.address,"port":request.port,"path":request.path,
        "query_redacted":request.query,"request_body_bytes":request.body});
    if !allowed(config, request.address, request.port) {
        let mut record = base.clone();
        record["decision"] = json!("denied");
        record["reason"] = json!("transport_policy_denied");
        spool.record("mediated_proxy", record)?;
        refuse(&mut client, 403);
        return Ok(());
    }
    // Admission is durable before the origin sees anything.
    let mut admitted = base.clone();
    admitted["decision"] = json!("allowed");
    spool.record("mediated_proxy", admitted)?;
    let outcome = relay(&request, &mut client, early);
    let mut end = base;
    end["type"] = json!("request_end");
    end["duration_us"] = json!(started.elapsed().as_micros() as u64);
    match outcome {
        Ok((status, bytes)) => {
            end["status"] = json!(status);
            end["response_bytes"] = json!(bytes);
        }
        Err(e) => {
            refuse(&mut client, 502);
            end["status"] = json!(502);
            end["error"] = json!(e.kind().to_string());
        }
    }
    spool.record("mediated_proxy", end)
}

fn relay(request: &Request, client: &mut TcpStream, early: Vec<u8>) -> io::Result<(u16, u64)> {
    let mut origin = TcpStream::connect_timeout(
        &SocketAddrV4::new(request.address, request.port).into(),
        TIMEOUT,
    )?;
    origin.set_read_timeout(Some(TIMEOUT))?;
    origin.set_write_timeout(Some(TIMEOUT))?;
    origin.set_nodelay(true)?;
    let mut head = format!(
        "{} {} HTTP/1.1\r\nHost: {}\r\n",
        request.method, request.target, request.authority
    );
    for (n, v) in &request.headers {
        head.push_str(&format!("{n}: {v}\r\n"));
    }
    if request.body > 0 || request.method == "POST" || request.method == "PUT" {
        head.push_str(&format!("Content-Length: {}\r\n", request.body));
    }
    head.push_str("Connection: close\r\n\r\n");
    let mut out = head.into_bytes();
    let early_len = (early.len() as u64).min(request.body);
    out.extend_from_slice(&early[..early_len as usize]);
    origin.write_all(&out)?;
    let remaining = request.body - early_len;
    if remaining > 0 {
        let copied = io::copy(&mut (&mut *client).take(remaining), &mut origin)?;
        if copied != remaining {
            return Err(io::Error::from(io::ErrorKind::UnexpectedEof));
        }
    }
    // Relay the whole response; the origin closes after it (Connection: close).
    let mut buf = [0u8; 64 * 1024];
    let (mut status, mut total, mut first) = (0u16, 0u64, Vec::new());
    loop {
        let n = origin.read(&mut buf)?;
        if n == 0 {
            break;
        }
        if first.len() < 16 {
            first.extend_from_slice(&buf[..n.min(16)]);
            if let Some(code) = std::str::from_utf8(&first)
                .ok()
                .and_then(|l| l.split(' ').nth(1))
                .and_then(|c| c.get(..3))
                .and_then(|c| c.parse().ok())
            {
                status = code;
            }
        }
        client.write_all(&buf[..n])?;
        total += n as u64;
    }
    if total == 0 {
        return Err(io::Error::from(io::ErrorKind::UnexpectedEof));
    }
    Ok((status, total))
}

/// Serves until the listener fails. A spool failure stops the proxy: no
/// request is ever forwarded without its durable record.
pub fn serve(config: &Config, listener: TcpListener, spool: &mut Spool) -> io::Result<()> {
    if config.subscription_domains.is_some() {
        return Err(io::Error::other(
            "subscription proxy requires pre-confinement vendor resolution",
        ));
    }
    let mut id = 0u64;
    for client in listener.incoming() {
        let Ok(client) = client else { continue };
        id += 1;
        serve_one(config, spool, client, id)?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn absolute_form_is_rewritten_to_origin_form() {
        let r = parse(
            "POST http://10.250.0.2:7003/x?secret=1 HTTP/1.1\r\nHost: 198.18.240.1:3128\r\nProxy-Authorization: x\r\nConnection: close, X-Hop\r\nX-Hop: y\r\nX-Lamp-Run: abc\r\nContent-Length: 32",
        )
        .unwrap();
        assert_eq!(r.address, "10.250.0.2".parse::<Ipv4Addr>().unwrap());
        assert_eq!(
            (r.port, r.target.as_str(), r.path.as_str(), r.query, r.body),
            (7003, "/x?secret=1", "/x", true, 32)
        );
        assert_eq!(
            r.headers,
            vec![("X-Lamp-Run".to_string(), "abc".to_string())]
        );
        assert_eq!(parse("GET http://1.1.1.1 HTTP/1.1").unwrap().target, "/");
        assert_eq!(
            parse("GET http://1.1.1.1?q HTTP/1.1").unwrap().target,
            "/?q"
        );
    }

    #[test]
    fn unsafe_requests_are_refused() {
        for (head, status) in [
            ("CONNECT 1.1.1.1:443 HTTP/1.1", 405),
            ("GET http://example.com/ HTTP/1.1", 400),
            ("GET http://u@1.1.1.1/ HTTP/1.1", 400),
            ("GET /relative HTTP/1.1", 400),
            ("GET https://1.1.1.1/ HTTP/1.1", 400),
            ("GET http://1.1.1.1:0/ HTTP/1.1", 400),
            ("GET http://1.1.1.1:99999/ HTTP/1.1", 400),
            ("GET http://1.1.1.1/#f HTTP/1.1", 400),
            ("GET http://1.1.1.1/ HTTP/2", 505),
            (
                "POST http://1.1.1.1/ HTTP/1.1\r\nTransfer-Encoding: chunked",
                501,
            ),
            (
                "POST http://1.1.1.1/ HTTP/1.1\r\nContent-Length: 1\r\nContent-Length: 2",
                400,
            ),
            ("POST http://1.1.1.1/ HTTP/1.1\r\nContent-Length: +1", 400),
            ("GET http://1.1.1.1:+80/ HTTP/1.1", 400),
            ("GET http://1.1.1.1/ HTTP/1.1\r\n folded", 400),
            ("GET http://1.1.1.1/ HTTP/1.1\r\nBad Name: x", 400),
        ] {
            assert_eq!(parse(head).unwrap_err().0, status, "{head}");
        }
    }

    #[test]
    fn policy_matches_the_guard() {
        let mut c: Config = serde_json::from_str(
            r#"{"attribution":{"host_id":"h","sandbox_id":"s","generation":1,"principal_id":"p","session_id":"ss","task_id":"t","operation_id":"o","policy_revision":1},
            "interface":"lamp0","guest_ip":"198.18.240.2","queue":1,"guard_uid":900001,
            "host_denies":["10.250.0.0/30"],"spool":"x","spool_bytes":8192}"#,
        )
        .unwrap();
        let ip = |s: &str| s.parse::<Ipv4Addr>().unwrap();
        assert!(allowed(&c, ip("1.1.1.1"), 80));
        assert!(!allowed(&c, ip("10.250.0.2"), 7003));
        assert!(!allowed(&c, ip("198.18.240.1"), 3128));
        assert!(!allowed(&c, ip("169.254.169.254"), 80));
        c.exceptions.push(crate::Exception {
            address: ip("10.250.0.2"),
            transport: "tcp".into(),
            port: 7003,
        });
        assert!(allowed(&c, ip("10.250.0.2"), 7003));
        assert!(!allowed(&c, ip("10.250.0.2"), 7004));
        c.policy_layers = serde_json::from_value(json!([{
            "filter_mode":"transport","default_egress":"public",
            "deny":[{"cidr":"10.250.0.2/32","protocol":"tcp","ports":[7003]}]
        }]))
        .unwrap();
        assert!(
            !allowed(&c, ip("10.250.0.2"), 7003),
            "proxy cannot bypass rule denies through an exception"
        );
        c.policy_layers.clear();
        c.safety_denies.push(crate::Prefix(ip("10.250.0.2"), 32));
        assert!(!allowed(&c, ip("10.250.0.2"), 7003));
    }
}
