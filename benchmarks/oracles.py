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
    return errors


def check_recovery(observed, history, authorized_producers):
    errors = []
    objects = observed.get("objects", {})
    for ack in history:
        identity = ack["id"]
        value = objects.get(identity)
        if value is None:
            errors.append("lost acknowledged object " + identity)
            continue
        if value.get("producer") not in authorized_producers:
            errors.append("unauthorized capture producer " + identity)
        if value.get("parent") != ack["parent"]:
            errors.append("ancestry mismatch " + identity)
        parent = ack["parent"]
        if parent is not None and parent not in objects:
            errors.append("lost ancestor " + parent)
        errors += [identity + ": " + e for e in check_state(
            value, ack["nonce"], ack["generation"], ack["memory_bytes"], ack["disk_bytes"]
        )]
    for identity, value in objects.items():
        if value.get("producer") not in authorized_producers:
            errors.append("unauthorized published capture " + identity)
    return errors
