#!/usr/bin/env python3
"""Copy a (sparse) disk image between Macs without filling the target.

  sparse-copy.py send FILE | ssh other sparse-copy.py recv FILE SIZE
Sends only the nonzero 1 MiB blocks (offset + length + bytes); the receiver
seeks over the holes, so the copy stays sparse on APFS.
"""
import os
import struct
import sys

B = 1 << 20
if sys.argv[1] == "send":
    zero = bytes(B)
    out = sys.stdout.buffer
    with open(sys.argv[2], "rb") as f:
        off = 0
        while chunk := f.read(B):
            if chunk != zero[: len(chunk)]:
                out.write(struct.pack("<QI", off, len(chunk)) + chunk)
            off += len(chunk)
else:
    path, size = sys.argv[2], int(sys.argv[3])
    inp = sys.stdin.buffer
    with open(path + ".partial", "wb") as f:
        while head := inp.read(12):
            off, n = struct.unpack("<QI", head)
            f.seek(off)
            f.write(inp.read(n))
        f.truncate(size)
    os.rename(path + ".partial", path)
