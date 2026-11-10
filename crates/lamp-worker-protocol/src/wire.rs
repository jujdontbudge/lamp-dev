//! Schema-free Protobuf wire comparison used to detect unknown fields.
//!
//! prost drops unknown fields on decode, so a received frame is re-encoded
//! and compared with the bytes that arrived. Byte equality is too strict:
//! map entries (and other repeated fields) may legally arrive in any order,
//! and Go's encoder does not sort maps. Two encodings are equivalent here when
//! every message holds the same multiset of fields, comparing nested messages
//! the same way. A field present in the input but dropped by the decoder
//! (unknown, or a duplicate scalar where the last one wins) makes the two
//! differ, so it is still rejected.

const MAX_DEPTH: usize = 32;

#[derive(Debug, Clone, Copy)]
struct Field<'a> {
    key: u64,
    /// The encoded value (for length-delimited fields, only the payload).
    value: &'a [u8],
}

fn varint(buf: &[u8], pos: &mut usize) -> Option<u64> {
    let mut v = 0_u64;
    for shift in (0..64).step_by(7) {
        let b = *buf.get(*pos)?;
        *pos += 1;
        v |= u64::from(b & 0x7f) << shift;
        if b & 0x80 == 0 {
            return Some(v);
        }
    }
    None
}

/// Splits one message into its top-level fields. `None` if it is not a
/// well-formed message (groups are not used by lamp.worker/1).
fn fields(buf: &[u8]) -> Option<Vec<Field<'_>>> {
    let mut out = Vec::new();
    let mut pos = 0;
    while pos < buf.len() {
        let key = varint(buf, &mut pos)?;
        if key >> 3 == 0 {
            return None;
        }
        let start = pos;
        let value = match key & 7 {
            0 => {
                varint(buf, &mut pos)?;
                &buf[start..pos]
            }
            1 => {
                pos = pos.checked_add(8).filter(|&p| p <= buf.len())?;
                &buf[start..pos]
            }
            5 => {
                pos = pos.checked_add(4).filter(|&p| p <= buf.len())?;
                &buf[start..pos]
            }
            2 => {
                let len = usize::try_from(varint(buf, &mut pos)?).ok()?;
                let end = pos.checked_add(len).filter(|&e| e <= buf.len())?;
                let v = &buf[pos..end];
                pos = end;
                v
            }
            _ => return None,
        };
        out.push(Field { key, value });
    }
    Some(out)
}

fn same_value(a: &[u8], b: &[u8], wire_type: u64, depth: usize) -> bool {
    if a == b {
        return true;
    }
    // Only a nested message may differ byte-wise yet be equal.
    wire_type == 2 && depth < MAX_DEPTH && same_message(a, b, depth + 1)
}

fn same_message(a: &[u8], b: &[u8], depth: usize) -> bool {
    let (Some(mut fa), Some(fb)) = (fields(a), fields(b)) else {
        return false;
    };
    if fa.len() != fb.len() {
        return false;
    }
    // Multiset match: every field of `b` consumes one equal field of `a`.
    for f in &fb {
        let Some(i) = fa
            .iter()
            .position(|g| g.key == f.key && same_value(g.value, f.value, f.key & 7, depth))
        else {
            return false;
        };
        fa.swap_remove(i);
    }
    true
}

/// True when `a` and `b` encode the same fields, ignoring the order of
/// fields within each (nested) message.
pub fn equivalent(a: &[u8], b: &[u8]) -> bool {
    a == b || same_message(a, b, 0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{Request, Start, request};
    use prost::Message;

    fn len_field(no: u64, payload: &[u8]) -> Vec<u8> {
        let mut out = vec![];
        prost::encoding::encode_key(
            no as u32,
            prost::encoding::WireType::LengthDelimited,
            &mut out,
        );
        prost::encoding::encode_varint(payload.len() as u64, &mut out);
        out.extend_from_slice(payload);
        out
    }

    fn entry(k: &str, v: &str) -> Vec<u8> {
        let mut e = len_field(1, k.as_bytes());
        e.extend(len_field(2, v.as_bytes()));
        len_field(3, &e)
    }

    /// Builds `Request{command: Start{argv:["env"], env}}` with env entries
    /// in exactly the given order, as a Go encoder may send them.
    fn start_with(entries: &[(&str, &str)], extra: &[u8]) -> Vec<u8> {
        let mut start = len_field(1, b"env");
        for (k, v) in entries {
            start.extend(entry(k, v));
        }
        start.extend_from_slice(extra);
        // Request.start is field 12 in worker.proto's command oneof.
        let tag = {
            let r = Request {
                context: None,
                command: Some(request::Command::Start(Start::default())),
            };
            let bytes = r.encode_to_vec();
            let mut pos = 0;
            varint(&bytes, &mut pos).unwrap() >> 3
        };
        len_field(tag, &start)
    }

    #[test]
    fn map_order_is_accepted_and_unknown_fields_are_not() {
        let order_a = start_with(&[("A", "1"), ("B", "2"), ("C", "3")], &[]);
        let order_b = start_with(&[("C", "3"), ("A", "1"), ("B", "2")], &[]);
        let decoded = Request::decode(order_b.as_slice()).unwrap();
        let reencoded = decoded.encode_to_vec();
        assert!(equivalent(&reencoded, &order_b), "reordered map accepted");
        assert!(equivalent(&order_a, &order_b));
        // An unknown field nested inside Start is dropped by decode → differs.
        let unknown = start_with(&[("A", "1")], &[0xf8, 0x01, 0x07]);
        let reencoded = Request::decode(unknown.as_slice()).unwrap().encode_to_vec();
        assert!(!equivalent(&reencoded, &unknown));
        // A duplicate map key is collapsed by decode → differs.
        let dup = start_with(&[("A", "1"), ("A", "2")], &[]);
        let reencoded = Request::decode(dup.as_slice()).unwrap().encode_to_vec();
        assert!(!equivalent(&reencoded, &dup));
    }

    #[cfg(unix)]
    #[test]
    fn socket_receive_accepts_go_map_order() {
        use std::io::Write;
        let request = start_with(&[("ZED", "1"), ("ALPHA", "2"), ("MID", "3")], &[]);
        let frame = len_field(2, &request); // Frame.request
        let (mut a, b) = std::os::unix::net::UnixStream::pair().unwrap();
        a.write_all(&(frame.len() as u32).to_be_bytes()).unwrap();
        a.write_all(&frame).unwrap();
        let (got, _) = crate::unix::recv_frame_with_fds(&b).unwrap();
        let Some(crate::frame::Payload::Request(r)) = got.payload else {
            panic!("not a request")
        };
        let Some(request::Command::Start(s)) = r.command else {
            panic!("not start")
        };
        assert_eq!(s.env.len(), 3);
        assert_eq!(s.env["ALPHA"], "2");
    }

    #[test]
    fn malformed_input_is_never_equivalent() {
        assert!(!equivalent(&[0x0a, 0x05, 0x01], &[0x0a, 0x01, 0x01]));
        assert!(equivalent(&[], &[]));
    }
}
