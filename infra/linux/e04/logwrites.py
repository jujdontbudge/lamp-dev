"""Power-cut model over a dm-log-writes log (Linux).

dm-log-writes records every bio that completes on the device it wraps, in
completion order, with its REQ_PREFLUSH / REQ_FUA flags, plus named marks
(drivers/md/dm-log-writes.c). A FLUSH entry is logged only after every write
that completed before it, so everything up to the last FLUSH before a crash
mark reached stable storage. FUA writes are stable on completion. Anything
else that completed after the last FLUSH was still in the device's volatile
cache when power was cut: `discard` drops all of it, `reorder` applies a
random subset in a random order. Both are states a real disk may legally be
left in.
"""
import os
import random
import struct

MAGIC = 0x6A736677736872
FLUSH, FUA, DISCARD, MARK, METADATA = 1, 2, 4, 8, 16
SUPER = struct.Struct("<QQQI")
ENTRY = struct.Struct("<QQQQ")


class Entry:
    __slots__ = ("index", "sector", "sectors", "flags", "mark", "offset")

    def __init__(self, index, sector, sectors, flags, mark, offset):
        self.index, self.sector, self.sectors = index, sector, sectors
        self.flags, self.mark, self.offset = flags, mark, offset


def read_log(path):
    """Returns (sector_size, entries). Data stays in the log file."""
    with open(path, "rb") as log:
        magic, version, count, sector_size = SUPER.unpack(log.read(SUPER.size))
        if magic != MAGIC or version != 1:
            raise ValueError("not a dm-log-writes log")
        entries, position = [], sector_size
        for index in range(count):
            log.seek(position)
            header = log.read(sector_size)
            sector, sectors, flags, length = ENTRY.unpack_from(header)
            mark = None
            if flags & MARK:
                mark = header[ENTRY.size:ENTRY.size + length].decode()
            entries.append(Entry(index, sector, sectors, flags, mark, position + sector_size))
            position += sector_size * (1 + sectors) if not flags & DISCARD else sector_size
        return sector_size, entries


def find_mark(entries, name):
    for entry in entries:
        if entry.mark == name:
            return entry.index
    raise ValueError("mark %r not in log" % name)


def is_write(entry):
    return entry.sectors and not entry.flags & (MARK | DISCARD)


def crash_state(entries, crash_index, fault, seed):
    """Entries a disk could hold at the crash: (durable, volatile_applied)."""
    before = entries[:crash_index]
    last_flush = max((e.index for e in before if e.flags & FLUSH), default=-1)
    durable = [e for e in before if e.index < last_flush and is_write(e)]
    volatile = []
    for e in before:
        if e.index < last_flush or not is_write(e):
            continue
        if e.flags & FUA:
            durable.append(e)
        else:
            volatile.append(e)
    if fault == "reorder":
        rng = random.Random(seed)
        volatile = [e for e in volatile if rng.random() < 0.5]
        rng.shuffle(volatile)
    elif fault == "discard":
        volatile = []
    else:
        raise ValueError("fault must be discard or reorder")
    # Durable writes keep completion order; the FUA ones may land later.
    durable.sort(key=lambda e: e.index)
    return durable, volatile, last_flush


def apply(log_path, sector_size, writes, image_path):
    with open(log_path, "rb") as log, open(image_path, "r+b") as image:
        for e in writes:
            log.seek(e.offset)
            data = log.read(e.sectors * sector_size)
            image.seek(e.sector * sector_size)
            image.write(data)
        image.flush()
        os.fsync(image.fileno())


def summary(entries, crash_index, durable, volatile, last_flush):
    return {
        "entries": len(entries), "crash_index": crash_index, "last_flush_index": last_flush,
        "flushes_before_crash": sum(1 for e in entries[:crash_index] if e.flags & FLUSH),
        "fua_before_crash": sum(1 for e in entries[:crash_index] if e.flags & FUA),
        "durable_writes": len(durable), "volatile_applied": len(volatile),
        "volatile_dropped": sum(1 for e in entries[:crash_index]
                                if e.index >= last_flush and is_write(e) and not e.flags & FUA) - len(volatile),
    }
