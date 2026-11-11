#[cfg(target_os = "macos")]
fn main() {
    match lamp_vz::native::read_task().and_then(lamp_vz::native::run) {
        Ok(code) => std::process::exit(code),
        Err(e) => {
            eprintln!("lamp-native: {e}");
            std::process::exit(1);
        }
    }
}

#[cfg(not(target_os = "macos"))]
fn main() {
    eprintln!("lamp-native requires macOS App Sandbox");
    std::process::exit(1);
}
