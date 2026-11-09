# Round 5: requirements responses

Status: complete, 2026-11-09. Revised [requirements.md](../requirements.md) against canonical [architecture.md](../architecture.md), its [round-5 handoff](5-responses-architecture.md), the binding brief, glossary and all nine ADRs. These are specification and document checks, not runtime, benchmark or hardware certification. Only requirements.md and this response file were edited; no commit, push, installation, paid service or password/approval prompt was run.

All 105 requirement IDs are retained. All O1–O10, A1–A5 and B1–B4 decisions are accepted; replaced O2 means no spending and three owned machines. macOS 26 is the certified minimum. There are no remaining owner questions. Engineering evidence and the out-of-scope brief version-example correction remain listed explicitly.

## Finding ledger

| Finding | Verdict | Change made | Requirement IDs |
| --- | --- | --- | --- |
| Y-001 | partly accepted | Propagated accepted B1–B4 into defaults, operative criteria, benchmarks and gates; removed pending votes. The suggested outstanding infrastructure cap is superseded by brief §22: three owned machines, no spending. | PRD-003, SBX-004, BEN-001–005, QA-001–004 |
| Y-002 | accepted | Made process_start/process_exec and actual executable/effective user canonical. Added typed PIDs, boot/start instance identities, parent/runtime/redaction and independent exported start/exec/user/parent assertions. | OPS-004, QA-003 |
| Y-003 | accepted | Completed attach envelope with result.stream; specified unwrapped read-only file-read, range hash, CLI --output metadata and MCP chunk/EOF/full hashes, including real artifact archive Blob. | INT-001, INT-003, INT-007, SES-003, STO-003 |
| Y-004 | accepted | Approval now displays a bounded immutable adapter-validated action_plan with server-derived digest; defined symbolic secret/form/diff bindings and deceptive-description/changed-effect tests. | ACC-008, CON-003, KIT-005, CRED-006 |
| Y-005 | accepted | Exposure create/get is pending_approval with no delivery. Active permission and activated_at delivery are distinct; denied/expired/unknown delivery and actual cookie/password location are tested. | KIT-002, KIT-005, SBX-006–007 |
| Y-006 | accepted | Added proof-key-bound bounded preauth auth_flow_bootstrap, exact challenge limits and clean job bootstrap→JWT→exchange; no precreated key or manual flow fixture. | CI-003, ACC-001, ACC-005 |
| Y-007 | accepted | Added independent owner-approved NativeExecutionGrant for exact host/user/paths/harness/expiry. A sandbox-only teleport key fails before any native staging/process effect. | SES-005, ACC-008–009 |
| Y-008 | accepted | Added sandbox_prepare_fork and child-bound Exposure approvals before raw-secret capture; recheck source revision/generation/full exposure history, consume once, expire empty child and inspect actual RAM. A1 subscriptions stay separate. | SBX-007, KIT-002, CRED-004 |
| Y-009 | accepted | Every public-repository CI job, including a trusted branch or unknown visibility, needs fresh VM/allowlist. Separately enforce stranger secret/cache/deletion limits; Exposure cannot waive admission. | SBX-006, CI-004–005, NET-001, QA-004 |
| Y-010 | accepted | Replaced acknowledgment-only badge with canonical per-host/enforcer revocation_status, authority expiry and redacted residual provider capability/revoke state; read during real partitions and after expiry. | ACC-009, OPS-001 |
| Y-011 | accepted | Use only event_list/event.list at GET /v1/events; SSE negotiates the same route with Accept. Removed the second operation/path and added parity coverage of the single binding. | INT-001, INT-005, ACC-009 |
| Y-012 | accepted | Copied the frozen lamp.compatibility/v1 positive case/lane/auth/device/provider pairs and exclusions; fixed required IDs cannot disappear with capability detection. Defined owned nested edition rows and case-removal gate. | HST-001, SBX-003–005, CRED-001–002, CI-001–002, QA-001–004 |
| Y-013 | accepted | Added volatile-write-loss/reordering faults, real per-OS stable-storage integration and deliberately omitted/reordered flush negative controls against an external acknowledgment oracle. Orderly restarts alone cannot qualify. | SBX-007, STO-005, QA-003 |
| Y-014 | accepted | Explained independent one-hour task deadline and seven-day saved-state retention. Canonical architecture requires new Task/Budget continuation after expiry, rather than reviving the stopped task; added exact resume fields/error, 24 h and concurrency/old-secret cases. | LIF-001–003, OPS-002 |
| Y-015 | accepted | Lifecycle on_job_end defaults delete; trusted retain_stopped retains only inert encrypted state with revoked job/network authority and no runner. Stranger always deletes; inspection uses new Task and next job stays fresh. | LIF-004, CI-004–005, STO-003 |
| Y-016 | accepted | Kept install/restart allocations and reliability confidence/2,995-trial/24 h objectives explicitly proposed. Proposed misses are reported; binding adoption needs dated baseline revision. Removed extra noncanonical performance thresholds. | PRD-002, QA-004, BEN-003, BEN-005 |
| Y-017 | accepted | Missing or failed fixed required owned-machine results block merge; visible blocked status is no waiver. Pending community native-arm64 speed certification is outside this matrix, as architecture specifies. | BEN-005, QA-004, ENG-001 |
| Y-018 | accepted | Added exact 32/33 mutation and 64/65 preauth boundaries, 2 s heartbeat, NTFS chain depth ≤2 and canonical replica/recovery/suspend observations on CLI/API/MCP/dashboard. | HST-004, STO-002, STO-005, LIF-002, OPS-009, QA-004 |
| Y-019 | accepted | All executable expiry fixtures derive from authoritative t0 and TTL; historical timestamps are illustrative. Test before/at/after expiry; approval 60 s, link default 3600 s and attach 30 s stay distinct. | ACC-007–008, NET-004, SES-003, QA-002–004 |
| Y-020 | accepted | Assigned all 44 attacks to canonical G/F/B/H/T or exact case sets. Linux Docker positives are separate from native/process fallback unsupported/denial variants; no impossible all-row suite or denial-for-positive substitution. | BEN-004, KIT-004, OPS-004, CI-001, QA-004 |
| Y-021 | accepted | Corrected Linux defaults/examples to 2 vCPU/4096 MiB/20 GiB/4096 PIDs. Native create reads actual Template minimum/default resources and rejects undersizing before reservation without silent growth. | SBX-003–004, TPL-003, BEN-001, QA-004 |
| Y-022 | accepted | Added concrete CLI/API/MCP device input/watch, exact device_ref/tagged units/sequences/backend acknowledgment/media shapes, key-down/up plus observed letter/pixel, PCM and USB response/reconnect/revoke fixtures. | INT-007, SBX-005, QA-004 |
| Y-023 | partly accepted | Preserved O7 fixed numeric counts/timings schema and 90-day expiry; reject version fields/arbitrary numeric labels. Explicitly resolves brief §20’s stale version example in favor of accepted O7. Editing brief.md itself is outside the two authorized outputs. | OPS-008, QA-004 |
| Y-024 | accepted | Share URL is secret-once: lost-response replay preserves Operation/grant and secret_available=false, never URL/new bearer. Scan outcomes/request hashes/events/audit; revoke and authorize a new link after loss. | NET-004, INT-004, ACC-009, QA-004 |
| Y-025 | accepted | Separated decoded payload and encoded wire caps: stream 64/96 KiB, bulk 1 MiB/1408 KiB, metadata 4 KiB, encoded queue 1 MiB and separate bulk buffer. Test serialized maxima/one-over/worst metadata; advertise both descriptor limits. | INT-005, INT-007, SES-003, OPS-009, QA-004 |
| Y-026 | accepted | Matched Linux request to hs_win/x86_64 and shared-files create to mode=shared_files. CRED-004 now reads the returned Snapshot; a separate Share create/read proves clean handoff. | SBX-002, STO-002, CRED-004 |
| Y-027 | accepted | Moved five plain stories/defaults near the top, added abbreviations and first-use expansions, and applied the ten concrete rewrites. Detailed calls remain in criteria and journey acceptance tests; shortened repeated E2E boilerplate. | INT-004, HST-004, SBX-007, STO-005, CRED-003–004, OPS-005, BEN-003, ENG-003, QA-004 |
| Y-028 | accepted | Added exact fleet_transfer_owner and local fleet_recover procedures, acceptance/recovery proofs, issuer/enforcer fencing, epoch/root/CA rotation, stable receipt/re-enrollment and rejection of all old authority. Data recovery remains separate. | ACC-005–006, ACC-009, STO-005, QA-004 |
| Y-029 | accepted | Added discoverable immutable Adapter/Configuration lifecycles and typed config_ref; upload package Blob, verify manifest/digest, discover schemas, authorize CredentialSource, create validated nonsecret config and test route from empty host on every interface. | PLG-001–002, NET-002–003, ACC-001–004, QA-004 |

Verdicts: **27 accepted, 2 partly accepted, 0 rebutted**. Partial acceptance rejects only the superseded infrastructure-cap sentence (Y-001) and records the unauthorized brief edit (Y-023); neither weakens accepted policy.

## Verification results

The reproducible script below found **382 set-difference entries before revision; all 382 are resolved, with 0 remaining**:

| Comparison | Before | After |
| --- | ---: | ---: |
| Operation IDs | 204 | 0 |
| HTTP method/path coverage | 160 | 0 |
| Error-code coverage | 3 | 0 |
| Lane names | 14 | 0 |
| Noncanonical literal path | 1 | 0 |
| **Total** | **382** | **0** |

This is a vocabulary/coverage count, not 382 independent behavioral bugs. The operation differences included 203 names previously only covered through lifecycle prose plus one removed event name; the explicit 56-resource/328-operation parity index now permits exhaustive comparison. Method/path omissions overlap those operation omissions. The lane differences include seven missing fixed lanes and seven retired spellings. The script checks literal example paths in addition to expanded operation routes.

Final result: 56 resource families; 328 canonical operations and 328 distinct method/path pairs; all canonical errors and 12 lane names; 434 inline/fenced JSON fragments parsed with 0 syntax failures (9 explicitly schematic fragments are not concrete payloads). All 105 IDs and 44 attack IDs are unique and retained. Separately checked all 23 fixed case IDs, their canonical assignment tables, all eight record sections, five owner stories and five detailed journey tests; Markdown table column counts are consistent. The index's standard bindings and copied extra-operation table match the canonical source. No product tests were run.

Requirement counts: PRD 3; INT 8; HST 6; SBX 7; TPL 3; LIF 6; STO 5; NET 6; PLG 2; CRED 6; CON 3; KIT 5; SES 7; ACC 9; RLY 2; CI 6; OPS 9; BEN 5; ENG 3; QA 4. **Total 105.**

## Reproduce the consistency check

From the repository root, extract and run the Python block below. It accepts an optional requirements snapshot path as its first argument; otherwise it checks the saved requirements.md. Exit 0 means no vocabulary/route/error/lane differences, JSON failures, missing requirement IDs or missing attack IDs. It reads files only.

```python
from pathlib import Path
from collections import Counter
import json, re, sys

ROOT = Path.cwd()
a = (ROOT / 'docs/spec/architecture.md').read_text()
r = Path(sys.argv[1] if len(sys.argv)>1 else ROOT / 'docs/spec/requirements.md').read_text()

def resources(text):
    result = {}
    for line in text.splitlines():
        m = re.match(r'\| `([a-z_]+)` / `([^`]+)` / `([a-z_]+)` \| `(/[^`]+)`(?: singleton)? \| ([^|]+) \|', line)
        if not m:
            continue
        kind, noun, stem, collection, actions = m.groups()
        collection = collection if collection.startswith('/v1/') else '/v1'+collection
        if re.search(r'\bR\b', actions):
            verbs = ['get', 'list'] + [v for k,v in [('C','create'),('U','update'),('D','delete')] if re.search(r'\b'+k+r'\b',actions)]
        elif '`' in actions:
            verbs = [op[len(stem)+1:] for op in re.findall(r'`([a-z_]+)`',actions)]
        else:
            verbs = re.findall(r'get|list|create|update|delete',actions)
        result[stem] = (kind, noun, collection, verbs)
    return result

ar = resources(a)
canonical = {stem+'_'+v for stem,(_,_,_,verbs) in ar.items() for v in verbs}
extra = a[a.index('| Extra operation IDs / MCP tools'):a.index('Scope actions are exactly')]
for line in extra.splitlines():
    if line.startswith('| `'):
        canonical.update(re.findall(r'`([a-z]+(?:_[a-z]+)+)`',line.split('|')[1]))
stems = sorted(set(ar)|{'api','docs','metric'}, key=len, reverse=True)
verbs = {op[len(stem)+1:] for op in canonical for stem in stems if op.startswith(stem+'_')}
operation_pattern = r'\b(?:'+'|'.join(stems)+r')_(?:'+'|'.join(sorted(verbs|{'read'}, key=len, reverse=True))+r')\b'
ro = set(re.findall(operation_pattern,r))
# Expand the standard table independently from its CLI noun/collection/verb columns.
rr = resources(r)
for stem,(_,_,_,actions) in rr.items():
    ro.update(stem+'_'+v for v in actions)

def route(op, table):
    exceptions = {
        'api_describe':('GET','/v1/openapi.json'), 'docs_read':('GET','/v1/docs'),
        'metric_list':('GET','/v1/metrics'), 'auth_flow_bootstrap':('POST','/v1/auth-flows:bootstrap'),
        'audit_record_export':('POST','/v1/audit-records:export'), 'telemetry_purge':('POST','/v1/telemetry:purge')}
    if op in exceptions:
        return exceptions[op]
    stem = next(s for s in stems if op.startswith(s+'_'))
    action = op[len(stem)+1:]
    # Baseline documents without an explicit index use the canonical common grammar.
    collection = table.get(stem,ar[stem])[2]
    item = collection if stem=='telemetry' else collection+'/{id}'
    if action in ('get','list','create','update','delete'):
        method={'get':'GET','list':'GET','create':'POST','update':'PATCH','delete':'DELETE'}[action]
        return method, collection if action in ('list','create') else item
    if op in ('exec_watch','session_watch'):
        return 'GET',item+'/output'
    if op=='sandbox_device_watch':
        return 'GET',item+'/device-stream'
    if op=='operation_wait':
        return 'GET',item+'/wait'
    if op in ('blob_download','artifact_download','blob_upload'):
        return ('PUT' if op=='blob_upload' else 'GET'),item+'/content'
    return 'POST',item+':'+action.replace('_','-')

ap = {route(op,ar) for op in canonical}
rp = {route(op,rr) for op in ro & canonical}
paths = {path for _,path in ap}
raw_paths = set(re.findall(r'/v1/[A-Za-z0-9_{}<>:./-]+',r))
bad_paths = set()
for path in raw_paths:
    path = path.rstrip('.')
    if path.startswith('/v1/<'):
        continue  # explicitly schematic collection grammar
    normalized = path
    for _,_,collection,_ in ar.values():
        if path.startswith(collection+'/'):
            tail=path[len(collection)+1:]
            if tail and not tail.startswith('{id}'):
                tail = re.sub(r'^(?:<[^>]+>|[A-Za-z0-9_-]+)', '{id}', tail)
            normalized=collection+'/'+tail
            break
    if normalized not in paths:
        bad_paths.add(path)
    else:
        # Concrete paths are checked even when the operation is not quoted nearby.
        rp.update(pair for pair in ap if pair[1]==normalized)

error_section = a[a.index('| HTTP / code'):a.index('Every update and dangerous extra action')]
ae = set(re.findall(r'`([A-Z][A-Z0-9_]+)`',error_section))
re_ = set(re.findall(r'`([A-Z][A-Z0-9_]+)`',r)) & (ae | set(re.findall(r'\b[A-Z][A-Z0-9_]*(?:_DENIED|_EXPIRED|_UNAVAILABLE|_CONFLICT|_REQUIRED|_EXCEEDED)\b',r)))
al = {'LX','LX22','D12','F40','L510','W11','WS22S','WS22D','LA','M5','AIR','NV'}
retired = {'WN','WP','WS','MIN','NV-LX','NV-Mac','NV-Windows'}
rl = set(re.findall(r'(?<![\w-])(?:'+'|'.join(sorted(al|retired,key=len,reverse=True))+r')(?![\w-])',r))

def delta(expected,actual):
    return {'missing':sorted(expected-actual), 'extra':sorted(actual-expected)}
diffs = {
    'operations':delta(canonical,ro),
    'method_paths':delta({m+' '+p for m,p in ap},{m+' '+p for m,p in rp}),
    'errors':delta(ae,re_), 'lanes':delta(al,rl),
    'noncanonical_literal_paths':sorted(bad_paths)}
count = sum(len(v['missing'])+len(v['extra']) if isinstance(v,dict) else len(v) for v in diffs.values())
# Syntax checks for complete inline/fenced examples. Schematic substitutions are explicit.
decoder=json.JSONDecoder(); valid=0; schematic=0; failures=[]
for line_no,line in enumerate(r.splitlines(),1):
    for fragment in re.findall(r'`([^`]+)`',line):
        for match in re.finditer(r'\{\s*"|\[\s*"',fragment):
            chunk=fragment[match.start():]
            if re.search(r':\s*<|\.\.\.',chunk):
                schematic+=1; continue
            try:
                decoder.raw_decode(chunk); valid+=1
            except json.JSONDecodeError as exc:
                failures.append((line_no,str(exc)))
for block in re.findall(r'```json\n(.*?)\n```',r,re.S):
    try: json.loads(block); valid+=1
    except json.JSONDecodeError as exc: failures.append(('fence',str(exc)))
ids=re.findall(r'^#### ([A-Z]+-\d{3}) —',r,re.M)
attacks=re.findall(r'^\| (ATK-\d{2}) \|',r,re.M)
print(json.dumps({'resources':len(rr),'canonical_operations':len(canonical),'canonical_method_paths':len(ap),
    'mismatch_count':count,'diff_counts':{k:(len(v['missing'])+len(v['extra']) if isinstance(v,dict) else len(v)) for k,v in diffs.items()},
    'diffs':diffs,'json_examples_parsed':valid,'schematic_fragments':schematic,'json_failures':failures,
    'requirements':len(ids),'unique_requirements':len(set(ids)),'area_counts':dict(sorted(Counter(x.split('-')[0] for x in ids).items())),
    'attacks':len(attacks),'unique_attacks':len(set(attacks))},indent=2))
sys.exit(1 if count or failures or len(set(ids))!=105 or set(attacks)!={f'ATK-{n:02}' for n in range(1,45)} else 0)
```
