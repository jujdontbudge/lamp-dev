/// Engine failure carrying a public `ErrorCode` value.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct EngineError {
    pub code: &'static str,
    pub message: String,
    pub retryable: bool,
}

pub type Result<T> = std::result::Result<T, EngineError>;

impl EngineError {
    pub fn new(code: &'static str, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
            retryable: false,
        }
    }
    pub fn invalid(message: impl Into<String>) -> Self {
        Self::new("INVALID_ARGUMENT", message)
    }
    pub fn not_found(message: impl Into<String>) -> Self {
        Self::new("NOT_FOUND", message)
    }
    pub fn in_use(message: impl Into<String>) -> Self {
        Self::new("RESOURCE_IN_USE", message)
    }
    pub fn host(message: impl Into<String>) -> Self {
        Self {
            code: "HOST_UNAVAILABLE",
            message: message.into(),
            retryable: true,
        }
    }
    pub fn context(self, what: &str) -> Self {
        Self {
            message: format!("{what}: {}", self.message),
            ..self
        }
    }
}

impl std::fmt::Display for EngineError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}: {}", self.code, self.message)
    }
}

impl std::error::Error for EngineError {}

impl From<std::io::Error> for EngineError {
    fn from(e: std::io::Error) -> Self {
        Self::host(e.to_string())
    }
}

/// Adds context to io results.
pub trait IoContext<T> {
    fn ctx(self, what: impl AsRef<str>) -> Result<T>;
}

impl<T> IoContext<T> for std::io::Result<T> {
    fn ctx(self, what: impl AsRef<str>) -> Result<T> {
        self.map_err(|e| EngineError::host(format!("{}: {e}", what.as_ref())))
    }
}
