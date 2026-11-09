use lamp_worker_protocol::*;
use std::io;
fn main() -> Result<(), Box<dyn std::error::Error>> {
    let input = read_frame(&mut io::stdin())?;
    let Some(frame::Payload::Request(request)) = input.payload else {
        return Err("request required".into());
    };
    let context = request.context.ok_or("context required")?;
    if context.request_id != "request-fixture"
        || context.operation_id != "operation-fixture"
        || context.deadline_ms != u64::MAX
        || context.generation != u64::MAX
        || context.policy_revision != u64::MAX
        || context.grant_id != "grant-fixture"
        || !matches!(request.command, Some(request::Command::Inspect(_)))
    {
        return Err("request changed across languages".into());
    }
    let frame = Frame {
        payload: Some(frame::Payload::Response(Response {
            context: Some(context),
            result: Some(response::Result::Receipt(Receipt {
                state: "accepted".into(),
                committed_handle_ids: vec![],
                outcome_unknown: false,
            })),
        })),
    };
    write_frame(&mut io::stdout(), &frame)?;
    Ok(())
}
