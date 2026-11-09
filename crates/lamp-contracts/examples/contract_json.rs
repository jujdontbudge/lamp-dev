use lamp_contracts::{Scope, SessionInputInput};
use std::io;
fn main() -> Result<(), Box<dyn std::error::Error>> {
    match std::env::args().nth(1).as_deref() {
        Some("input-json") => {
            let value: SessionInputInput = serde_json::from_reader(io::stdin())?;
            serde_json::to_writer(io::stdout(), &value)?;
        }
        Some("scope-json") => {
            let value: Scope = serde_json::from_reader(io::stdin())?;
            serde_json::to_writer(io::stdout(), &value)?;
        }
        _ => panic!("mode required"),
    }
    Ok(())
}
