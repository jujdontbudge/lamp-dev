//! A writable, identity-mapped view of the sandbox disk for subordinate task root.
//! Runtime mounts retain their original identities: the tracer, helper and audit
//! state under /run/lamp remain owned by guest kernel root and inaccessible.
use std::{
    fs, io,
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
    ptr,
};

fn checked(value: libc::c_long) -> io::Result<libc::c_long> {
    if value < 0 {
        Err(io::Error::last_os_error())
    } else {
        Ok(value)
    }
}

pub fn mapping(uid: u32) -> io::Result<OwnedFd> {
    // This helper is made before the collector starts any threads or tracees.
    let child = unsafe { libc::fork() };
    checked(child as _)?;
    if child == 0 {
        unsafe {
            if libc::unshare(libc::CLONE_NEWUSER) != 0 {
                libc::_exit(126);
            }
            libc::raise(libc::SIGSTOP);
            libc::_exit(0);
        }
    }
    let result = (|| {
        let mut status = 0;
        checked(unsafe { libc::waitpid(child, &mut status, libc::WUNTRACED) } as _)?;
        if !libc::WIFSTOPPED(status) {
            return Err(io::Error::other("rootfs identity namespace failed"));
        }
        fs::write(format!("/proc/{child}/setgroups"), "deny")?;
        // Map disk UID 0 to task kernel UID `uid`. Include
        // distribution system users so apt can drop to _apt and chown files.
        let map = format!("0 {uid} 65536\n");
        fs::write(format!("/proc/{child}/uid_map"), &map)?;
        fs::write(format!("/proc/{child}/gid_map"), &map)?;
        use std::os::fd::IntoRawFd;
        Ok(unsafe {
            OwnedFd::from_raw_fd(fs::File::open(format!("/proc/{child}/ns/user"))?.into_raw_fd())
        })
    })();
    unsafe {
        libc::kill(child, libc::SIGKILL);
        libc::waitpid(child, ptr::null_mut(), 0);
    }
    result
}

/// Called in the trusted child, after its mount namespace is private and before
/// dropping into the task user namespace. No ancestor-root directory FD survives.
pub fn enter(namespace: &OwnedFd) -> io::Result<String> {
    #[repr(C)]
    struct Attr {
        set: u64,
        clear: u64,
        propagation: u64,
        userns: u64,
    }
    // Create on the original disk: kernel root is deliberately unmapped in
    // the task view and cannot create new inodes through that view.
    let old = format!(".lamp-old-{}", std::process::id());
    fs::create_dir(format!("/{old}"))
        .map_err(|e| io::Error::other(format!("old root directory: {e}")))?;
    let mount = checked(unsafe {
        libc::syscall(
            libc::SYS_open_tree,
            libc::AT_FDCWD,
            c"/".as_ptr(),
            1 | libc::O_CLOEXEC,
        )
    })?;
    let mount = unsafe { OwnedFd::from_raw_fd(mount as _) };
    let attr = Attr {
        set: 0x00100000,
        clear: 0,
        propagation: 0,
        userns: namespace.as_raw_fd() as u64,
    };
    checked(unsafe {
        libc::syscall(
            libc::SYS_mount_setattr,
            mount.as_raw_fd(),
            c"".as_ptr(),
            libc::AT_EMPTY_PATH,
            &attr,
            std::mem::size_of::<Attr>(),
        )
    })?;
    fs::create_dir_all("/run/lamp/root")
        .map_err(|e| io::Error::other(format!("root mount directory: {e}")))?;
    checked(unsafe {
        libc::syscall(
            libc::SYS_move_mount,
            mount.as_raw_fd(),
            c"".as_ptr(),
            libc::AT_FDCWD,
            c"/run/lamp/root".as_ptr(),
            4,
        )
    })?;
    // Keep the boot entry and future collectors protected on the writable
    // disk. Ancestor mountpoints cannot be renamed; individual trusted files
    // cannot be written or replaced. A subordinate user namespace cannot
    // detach these mounts or create an alternate raw-disk mount.
    for path in [
        "usr",
        "usr/sbin",
        "usr/sbin/lamp-init",
        "usr/sbin/lamp-guest-helper",
        "usr/sbin/lamp-guard",
    ] {
        let path = std::ffi::CString::new(format!("/run/lamp/root/{path}")).unwrap();
        checked(unsafe {
            libc::mount(
                path.as_ptr(),
                path.as_ptr(),
                ptr::null(),
                libc::MS_BIND,
                ptr::null(),
            )
        } as _)?;
        if path.to_bytes().ends_with(b"lamp-init")
            || path.to_bytes().ends_with(b"lamp-guest-helper")
            || path.to_bytes().ends_with(b"lamp-guard")
        {
            checked(unsafe {
                libc::mount(
                    ptr::null(),
                    path.as_ptr(),
                    ptr::null(),
                    libc::MS_BIND | libc::MS_REMOUNT | libc::MS_RDONLY,
                    ptr::null(),
                )
            } as _)?;
        }
    }
    for (source, target, recursive) in [
        (c"/dev", c"/run/lamp/root/dev", true),
        (c"/sys", c"/run/lamp/root/sys", true),
        (c"/proc", c"/run/lamp/root/proc", false),
        (c"/run", c"/run/lamp/root/run", false),
    ] {
        checked(unsafe {
            libc::mount(
                source.as_ptr(),
                target.as_ptr(),
                ptr::null(),
                libc::MS_BIND | if recursive { libc::MS_REC } else { 0 },
                ptr::null(),
            )
        } as _)?;
    }
    checked(unsafe { libc::chdir(c"/run/lamp/root".as_ptr()) } as _)?;
    // A chroot would make the subsequent user-namespace admission fail with
    // EPERM. Replace the mount root instead and discard every ancestor mount.
    let old_c = std::ffi::CString::new(old.clone()).unwrap();
    checked(unsafe { libc::syscall(libc::SYS_pivot_root, c".".as_ptr(), old_c.as_ptr()) })?;
    checked(unsafe { libc::chdir(c"/".as_ptr()) } as _)?;
    let old_path = format!("/{old}");
    let old_c = std::ffi::CString::new(old_path.clone()).unwrap();
    checked(unsafe { libc::umount2(old_c.as_ptr(), libc::MNT_DETACH) } as _)?;
    // The admitted task removes this empty directory after its IDs are mapped.
    Ok(old_path)
}
