//! lamp.worker/1 length-prefixed control framing. Authentication and authority
//! validation belong to the receiving service, before command dispatch.
use prost::Message;
use std::io::{self, Read, Write};
include!("lamp.worker.v1.rs");
pub mod grant;
#[cfg(unix)]
pub mod unix;
pub const PROTOCOL: &str = "lamp.worker/1";
pub const MAX_CONTROL_BYTES: usize = 1 << 20;

pub fn read_frame(reader: &mut impl Read) -> io::Result<Frame> {
    let mut prefix = [0_u8; 4];
    reader.read_exact(&mut prefix)?;
    let size = u32::from_be_bytes(prefix) as usize;
    if size == 0 || size > MAX_CONTROL_BYTES {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid worker frame length",
        ));
    }
    let mut bytes = vec![0; size];
    reader.read_exact(&mut bytes)?;
    let frame = Frame::decode(bytes.as_slice())
        .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
    if frame.payload.is_none() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "missing worker payload",
        ));
    }
    Ok(frame)
}

pub fn write_frame(writer: &mut impl Write, frame: &Frame) -> io::Result<()> {
    let size = frame.encoded_len();
    if frame.payload.is_none() || size == 0 || size > MAX_CONTROL_BYTES {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "invalid worker frame length",
        ));
    }
    writer.write_all(&(size as u32).to_be_bytes())?;
    writer.write_all(&frame.encode_to_vec())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn roundtrip_and_limits() {
        let mut frame = Frame {
            payload: Some(frame::Payload::Hello(Hello {
                protocol: PROTOCOL.into(),
                worker_id: "worker".into(),
                boot_id: "boot".into(),
                max_control_bytes: MAX_CONTROL_BYTES as u32,
            })),
        };
        let mut data = Vec::new();
        write_frame(&mut data, &frame).unwrap();
        assert_eq!(read_frame(&mut data.as_slice()).unwrap(), frame);
        for length in [0, MAX_CONTROL_BYTES as u32 + 1, u32::MAX] {
            let bytes = length.to_be_bytes();
            let mut source = bytes.as_slice();
            assert_eq!(
                read_frame(&mut source).unwrap_err().kind(),
                io::ErrorKind::InvalidData
            );
            assert!(source.is_empty());
        }
        // Find an exact 1 MiB protobuf and exercise both sides of the bound.
        if let Some(frame::Payload::Hello(ref mut hello)) = frame.payload {
            hello.worker_id = "x".repeat(MAX_CONTROL_BYTES - 64);
        }
        let delta = MAX_CONTROL_BYTES - frame.encoded_len();
        if let Some(frame::Payload::Hello(ref mut hello)) = frame.payload {
            hello.worker_id.push_str(&"x".repeat(delta));
        }
        assert_eq!(frame.encoded_len(), MAX_CONTROL_BYTES);
        data.clear();
        write_frame(&mut data, &frame).unwrap();
        read_frame(&mut data.as_slice()).unwrap();
        if let Some(frame::Payload::Hello(ref mut hello)) = frame.payload {
            hello.worker_id.push('x');
        }
        data.clear();
        assert!(write_frame(&mut data, &frame).is_err());
        assert!(data.is_empty());
    }
    #[test]
    fn truncated_and_invalid() {
        for bytes in [
            vec![0, 0],
            vec![0, 0, 0, 2, 255],
            vec![0, 0, 0, 1, 255],
            vec![0, 0, 0, 2, 0x20, 1],
        ] {
            assert!(read_frame(&mut bytes.as_slice()).is_err());
        }
    }
}
