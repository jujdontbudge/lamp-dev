#!/usr/bin/env python3
"""Check the final spec; maintained from the round-5 requirements checker."""

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
al = {'LX','LX22','D12','F40','L510','LA','M5','AIR','NV'}
retired = {'WN','WP','WS','MIN','NV-LX','NV-Mac','NV-Windows','W11','WS22S','WS22D'}  # Owner removal record, 2026-11-09; reject retired lanes.
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
