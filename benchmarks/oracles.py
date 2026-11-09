import hashlib


def pattern_hash(nonce, generation, size, domain):
    result = hashlib.sha256()
    prefix = f"{nonce}:{generation}:{domain}:".encode()
    for offset in range(0, size, 4096):
        word = hashlib.sha256(prefix + str(offset // 4096).encode()).digest()
        result.update((word * 128)[:min(4096, size - offset)])
    return result.hexdigest()


def check_state(observed, nonce, generation, memory_bytes, disk_bytes=16384):
    errors = []
    expected = {
        "nonce": nonce, "generation": generation,
        "memory_sha256": pattern_hash(nonce, generation, memory_bytes, "ram"),
        "disk_sha256": pattern_hash(nonce, generation, disk_bytes, "disk"),
    }
    for key, value in expected.items():
        if observed.get(key) != value:
            errors.append(key + " mismatch")
    if observed.get("ticks", 0) <= 0:
        errors.append("resident process is not running")
    return errors


def check_fork(parent_before, parent_after, child, nonce, memory_bytes):
    errors = check_state(parent_after, nonce, 1, memory_bytes)
    errors += check_state(child, nonce, 2, memory_bytes)
    if parent_after.get("ticks", 0) <= parent_before.get("ticks", 0):
        errors.append("source did not continue")
    if child.get("ticks", 0) < parent_before.get("ticks", 0):
        errors.append("child did not continue")
    return errors


def check_recovery(observed, history, authorized_producers, written=None):
    errors = []
    objects = observed.get("objects", {})
    expected = {item["id"]: item for item in (history if written is None else written)}
    for ack in history:
        if ack["id"] not in objects:
            errors.append("lost acknowledged object " + ack["id"])
    for identity, value in objects.items():
        if value.get("producer") not in authorized_producers:
            errors.append("unauthorized published capture " + identity)
        record = expected.get(identity)
        if record is None:
            errors.append("unexpected recovered object " + identity)
            continue
        if value.get("parent") != record["parent"]:
            errors.append("ancestry mismatch " + identity)
        parent = record["parent"]
        if parent is not None and parent not in objects:
            errors.append("lost ancestor " + parent)
        errors += [identity + ": " + e for e in check_state(
            value, record["nonce"], record["generation"], record["memory_bytes"], record["disk_bytes"]
        )]
    return errors
