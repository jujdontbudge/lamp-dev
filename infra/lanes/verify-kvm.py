#!/usr/bin/env python3
"""Probe the nested KVM API without running a workload or installing packages."""
import fcntl
import os
fd = os.open('/dev/kvm', os.O_RDWR | os.O_CLOEXEC)
assert fcntl.ioctl(fd, 0xAE00, 0) == 12, 'Unexpected KVM API version'
vm = fcntl.ioctl(fd, 0xAE01, 0)
os.close(vm)
os.close(fd)
print('KVM API 12; KVM_CREATE_VM succeeded')
