use crate::engine::Engine;
use crate::{EngineError, Result};
use sha2::{Digest, Sha256};
use std::fs::{File, OpenOptions};
use std::io::{Read, Seek};
use std::os::fd::AsRawFd;
use std::os::unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt};
use std::path::PathBuf;
use std::process::{Command, Stdio};

struct Temporary(PathBuf);
impl Drop for Temporary {
    fn drop(&mut self) {
        let _ = std::fs::remove_file(&self.0);
    }
}

fn hash(file: &mut File) -> Result<String> {
    file.rewind()?;
    let mut digest = Sha256::new();
    let mut buffer = vec![0; 1 << 20];
    loop {
        let count = file.read(&mut buffer)?;
        if count == 0 {
            break;
        }
        digest.update(&buffer[..count]);
    }
    Ok(lamp_init::hex_encode(&digest.finalize()))
}

impl Engine {
    pub fn export_clean_template(
        &self,
        report: &crate::build::BuildReport,
        caller_uid: u32,
    ) -> Result<String> {
        let output = self.cfg.exports_dir.as_ref().ok_or_else(|| {
            EngineError::new(
                "CLEAN_REBUILD_REQUIRED",
                "clean export transport is not configured",
            )
        })?;
        let template = self.cfg.store.join("templates").join(&report.template_id);
        let verified = self.verify_template_dir(&template)?;
        if verified.id != report.template_id
            || verified.manifest.digest()? != report.manifest_digest
        {
            return Err(EngineError::new(
                "DATA_UNAVAILABLE",
                "clean reconstruction changed before export",
            ));
        }
        match std::fs::create_dir(output) {
            Ok(()) => std::fs::set_permissions(output, std::fs::Permissions::from_mode(0o700))?,
            Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => {}
            Err(error) => return Err(error.into()),
        }
        let directory = OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_DIRECTORY | libc::O_NOFOLLOW)
            .open(output)?;
        let metadata = directory.metadata()?;
        if metadata.mode() & 0o077 != 0 || (metadata.uid() != 0 && metadata.uid() != caller_uid) {
            return Err(EngineError::new(
                "SCOPE_DENIED",
                "export directory must belong to the authenticated control caller",
            ));
        }
        if unsafe { libc::fchown(directory.as_raw_fd(), caller_uid, 0) } != 0 {
            return Err(std::io::Error::last_os_error().into());
        }
        let path = self.cfg.store.join("custody").join(format!(
            "export-{}",
            lamp_init::hex_encode(&crate::fsutil::random_bytes::<16>())
        ));
        let mut archive = OpenOptions::new()
            .create_new(true)
            .read(true)
            .write(true)
            .mode(0o600)
            .open(&path)?;
        let _temporary = Temporary(path);
        let mut names = Vec::new();
        for entry in std::fs::read_dir(&template)? {
            let entry = entry?;
            if !entry.file_type()?.is_file() {
                return Err(EngineError::new(
                    "SCOPE_DENIED",
                    "only sealed reconstruction files may be exported",
                ));
            }
            names.push(entry.file_name());
        }
        names.sort();
        let status = Command::new("tar")
            .args([
                "--create",
                "--sparse",
                "--format=pax",
                "--file=-",
                "--directory",
            ])
            .arg(&template)
            .arg("--")
            .args(names)
            .stdin(Stdio::null())
            .stderr(Stdio::null())
            .stdout(Stdio::from(archive.try_clone()?))
            .status()?;
        if !status.success() {
            return Err(EngineError::host("clean export archiver failed"));
        }
        archive.sync_all()?;
        let digest = hash(&mut archive)?;
        let id = format!("bl_{digest}");
        let destination = output.join(format!("{id}.tar"));
        match OpenOptions::new()
            .create_new(true)
            .write(true)
            .mode(0o600)
            .open(&destination)
        {
            Ok(mut file) => {
                archive.rewind()?;
                std::io::copy(&mut archive, &mut file)?;
                file.sync_all()?;
                file.set_permissions(std::fs::Permissions::from_mode(0o400))?;
                if unsafe { libc::fchown(file.as_raw_fd(), caller_uid, 0) } != 0 {
                    return Err(std::io::Error::last_os_error().into());
                }
            }
            Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => {
                let mut file = OpenOptions::new()
                    .read(true)
                    .custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK)
                    .open(&destination)?;
                if !file.metadata()?.is_file() || hash(&mut file)? != digest {
                    return Err(EngineError::new(
                        "DATA_UNAVAILABLE",
                        "export object conflicts with its digest",
                    ));
                }
            }
            Err(error) => return Err(error.into()),
        }
        directory.sync_all()?;
        Ok(id)
    }
}
