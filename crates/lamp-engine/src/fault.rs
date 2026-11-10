//! Crash and omitted-barrier injection for the E04 storage proof.
//!
//! Only builds with the `fault-injection` feature carry it. A production
//! build compiles every hook to nothing, and `arm` refuses.
//!
//! When armed, the first commit boundary that matches `point`/`side` ends
//! the process at once with `_exit(79)`: no destructor, flush or reply runs,
//! exactly like losing the process. An `omit` barrier is skipped (for the
//! negative controls that must be caught).
use crate::error::{EngineError, Result};

pub const CRASH_EXIT: i32 = 79;

/// Commit boundaries, in order, shared with the harness (benchmarks/protocol.py).
pub const POINTS: &[&str] = &[
    "reserve",
    "capture",
    "seal",
    "object_flush",
    "directory_flush",
    "heads",
    "switch",
    "journal_flush",
    "publish",
    "ack",
    "delete",
    "gc",
    "compact_objects",
    "compact_redirect",
    "compact_gc",
];

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Spec {
    pub point: Option<String>,
    pub side: String,
    pub omit: Option<String>,
}

#[cfg(feature = "fault-injection")]
static ARMED: std::sync::Mutex<Option<Spec>> = std::sync::Mutex::new(None);

pub const ENABLED: bool = cfg!(feature = "fault-injection");

pub fn arm(spec: Spec) -> Result<()> {
    if let Some(p) = &spec.point
        && !POINTS.contains(&p.as_str())
        && p != "none"
    {
        return Err(EngineError::invalid(format!("unknown commit point {p}")));
    }
    if !matches!(spec.side.as_str(), "before" | "after") {
        return Err(EngineError::invalid("side must be before or after"));
    }
    if let Some(o) = &spec.omit
        && !matches!(
            o.as_str(),
            "object_flush" | "directory_flush" | "journal_flush"
        )
    {
        return Err(EngineError::invalid(format!("cannot omit {o}")));
    }
    #[cfg(feature = "fault-injection")]
    {
        *ARMED.lock().unwrap() = Some(spec);
        Ok(())
    }
    #[cfg(not(feature = "fault-injection"))]
    {
        let _ = spec;
        Err(EngineError::invalid(
            "this build carries no fault injection",
        ))
    }
}

/// Ends the process if the armed crash point is `point`/`side`.
#[inline]
pub fn hit(point: &str, side: &str) {
    #[cfg(feature = "fault-injection")]
    {
        let fire = ARMED.lock().unwrap().as_ref().is_some_and(|s| {
            s.point.as_deref() == Some(point) && s.side == side
        });
        if fire {
            eprintln!("lamp-engine: fault injection: crash at {point}/{side}");
            // SAFETY: immediate process exit, nothing else runs.
            unsafe { libc::_exit(CRASH_EXIT) };
        }
    }
    #[cfg(not(feature = "fault-injection"))]
    let _ = (point, side);
}

/// True when the armed spec says to skip this barrier.
#[inline]
pub fn omitted(barrier: &str) -> bool {
    #[cfg(feature = "fault-injection")]
    {
        let skip = ARMED
            .lock()
            .unwrap()
            .as_ref()
            .is_some_and(|s| s.omit.as_deref() == Some(barrier));
        if skip {
            eprintln!("lamp-engine: fault injection: omitting {barrier}");
        }
        skip
    }
    #[cfg(not(feature = "fault-injection"))]
    {
        let _ = barrier;
        false
    }
}

/// Runs the `before` hook, the step, then the `after` hook.
pub fn step<T>(point: &str, f: impl FnOnce() -> Result<T>) -> Result<T> {
    hit(point, "before");
    let v = f()?;
    hit(point, "after");
    Ok(v)
}
