//! lamp.worker/1 length-prefixed control framing. Authentication and authority
//! validation belong to the receiving service, before command dispatch.
use prost::Message;
use std::io::{self, Read, Write};
include!("lamp.worker.v1.rs");
pub mod grant;
#[cfg(unix)]
pub mod unix;
pub mod wire;
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
