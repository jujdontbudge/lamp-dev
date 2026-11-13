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

fn same_value(a: &[u8], b: &[u8], wire_type: u64, depth: usize, path: &[u64]) -> bool {
    if a == b {
        return true;
    }
    // Only a nested message may differ byte-wise yet be equal.
    wire_type == 2 && depth < MAX_DEPTH && same_message(a, b, depth + 1, path)
}

fn same_message(a: &[u8], b: &[u8], depth: usize, path: &[u64]) -> bool {
    let (Some(mut fa), Some(mut fb)) = (fields(a), fields(b)) else {
        return false;
    };
    if path == [2, 11, 3] || path == [3, 10, 5] {
        // Go writes empty string map values; prost omits them. Both are
        // valid Protobuf. Preserve unknown fields and duplicate detection.
        for fields in [&mut fa, &mut fb] {
            for key in [10, 18] {
                if fields.iter().filter(|f| f.key == key).count() == 1 {
                    fields.retain(|f| f.key != key || !f.value.is_empty());
                }
            }
        }
    }
    if fa.len() != fb.len() {
        return false;
    }
    // Multiset match: every field of `b` consumes one equal field of `a`.
    for f in &fb {
        let mut nested = path.to_vec();
        nested.push(f.key >> 3);
        let Some(i) = fa.iter().position(|g| {
            g.key == f.key && same_value(g.value, f.value, f.key & 7, depth, &nested)
        }) else {
            return false;
        };
        fa.swap_remove(i);
    }
    true
}

/// True when `a` and `b` encode the same fields, ignoring the order of
/// fields within each (nested) message.
pub fn equivalent(a: &[u8], b: &[u8]) -> bool {
    a == b || same_message(a, b, 0, &[])
}
