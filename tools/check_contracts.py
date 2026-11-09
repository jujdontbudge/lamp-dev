#!/usr/bin/env python3
"""Independent spec-name conformance. Does not import the generator."""
import hashlib
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def check(schema=None, architecture=None):
    schema = schema or json.loads((ROOT / 'contracts/public/schema.json').read_text())
    architecture = architecture or (ROOT / 'docs/spec/architecture.md').read_text()
    spec = '## 11. ' + architecture.split('## 11. ', 1)[1].split('## 13. ', 1)[0]
    defs = schema['$defs']
    resources = schema['x-lamp-resources']
    operations = schema['x-lamp-operations']
    expected_resources, expected_ops = {}, set()
    for line in spec.split('### 11.1 Resources')[1].split('### 11.2')[0].splitlines():
        match = re.match(r'\| `(\w+)` / `([^`]+)` / `([^`]+)` \| `([^`]+)`(.*?) \| ([^|]+) \| `([^`]+)`', line)
        if not match:
            continue
        kind, noun, stem, collection, singleton, actions, raw = match.groups()
        action_set = set()
        for action in actions.strip().split(','):
            action_set.update({'R':['get','list'], 'C':['create'], 'U':['update'], 'D':['delete']}.get(action, [action]))
        expected_resources[kind] = (noun, stem, '/v1'+collection, bool(singleton.strip()), action_set)
        actual = resources[kind]
        assert (actual['noun'], actual['stem'], actual['collection'], actual['singleton'], set(actual['actions'])) == expected_resources[kind], kind
        fields = set(re.findall(r'(?:^|,)(\w+)(?:\[\])?(?:\([^)]*\))?(?:=\d+)?(?=,|$)', raw))
        assert fields, kind
        props = defs[actual['schema']]['properties']
        assert fields <= set(props), (kind, 'missing fields', fields-set(props))
        assert props['kind']['const'] == kind, kind
        for field, values in re.findall(r'(\w+)\(([^)]+)\)', raw):
            assert set(props[field]['enum']) == set(values.replace('\\','').split('|')), (kind, field)
        for action in action_set:
            expected_ops.add(stem+'_'+action)
            op=operations[stem+'_'+action]
            assert op['method']=={'get':'get','list':'get','create':'post','update':'patch','delete':'delete'}[action]
            path='/v1'+collection
            if action in ['get','update','delete'] and not actual['singleton']:path+='/{'+stem+'_id}'
            assert op['path']==path,(stem,action)
    assert set(resources)==set(expected_resources), 'resource set drift'
    extras=spec.split('### 12.1')[1].split('Scope actions are exactly')[0]
    for line in extras.splitlines():
        if line.startswith('| `'):
            expected_ops.update(re.findall(r'`([a-z]+(?:_[a-z]+)+)`',line.split(' | ')[0]))
    assert set(operations)==expected_ops, ('operation drift',set(operations)^expected_ops)
    # Every explicit input name in the extra-action table must exist on at least
    # one of that row's named operations. Per-operation routes/scopes are checked below.
    for line in extras.splitlines():
        if not line.startswith('| `'):continue
        names=re.findall(r'`([a-z]+(?:_[a-z]+)+)`',line.split(' | ')[0])
        fields=set()
        for shape in re.findall(r'\{([^{}]*)\}',line):
            if shape=='id':continue
            # Uppercase describes a referenced record, not a JSON field.
            fields.update(re.findall(r'(?:^|,)\s*([a-z][a-z_]*)(?=\[\]|:|,|$)',shape))
        present=set()
        for name in names:
            op=operations[name]
            present.update(defs[op['input_schema']]['properties'])
            result=defs[op.get('completion_schema',op['result_schema'])]
            if '$ref' in result:result=defs[result['$ref'].split('/')[-1]]
            present.update(result.get('properties',{}))
        assert fields<=present,(names,'missing input/result names',fields-present)
    for name,op in operations.items():
        assert name==op['mcp']==op['kind']+'_'+op['action'],name
        assert op['scope']==op['kind']+'.'+op['action'],name
        noun=resources.get(op['kind'],{'noun':op['kind']})['noun']
        assert op['cli']=='lamp '+noun+' '+op['action'].replace('_','-'),name
        inp=defs[op['input_schema']]
        assert inp['additionalProperties'] is False,name
        assert set(inp.get('required',[]))<=set(inp['properties']),name
        if op['mutation']:
            assert 'idempotency_key' in inp['required'],name
            if op['action']!='create':assert 'expected_revision' in inp['required'],name
        else:assert 'idempotency_key' not in inp['properties'],name
        if op['kind'] in resources and op['action'] in ['get','update','delete'] and not resources[op['kind']]['singleton']:
            assert op['kind']+'_id' in inp['required'],name
    assert set(defs['Scope']['properties']['actions']['items']['enum'])=={o['scope'] for o in operations.values()},'scope action enum drift'
    # Nested fields with their own canonical shape, checked by context rather
    # than just searching for a same-named field somewhere else in the schema.
    shapes={
      'Resources':'vcpus memory_mib disk_gib pids_max',
      'Device':'id type model resource_ref options',
      'Lifecycle':'idle_action idle_grace_seconds suspended_retention_seconds expires_at on_job_end',
      'Location':'kind host_id sandbox_id',
      'Scope':'actions sandbox_ids recipe_ids host_ids resource_ids limits expires_at',
      'Limits':'vcpus memory_mib disk_gib concurrency wall_time_ms token_limit retained_bytes control_records',
      'Capacity':'vcpus memory_mib disk_gib pids_max retained_bytes control_records',
      'Readiness':'command_started_at agent_ready_at checks',
      'ReadinessCheck':'name state error',
      'ApprovalPolicy':'mode approver_ids ttl_seconds',
      'NetworkPlan':'type plugin_id config_ref port_maps',
      'NetworkRule':'host_pattern cidr protocol ports',
      'DNS':'resolver_mode strict_answers',
      'FlowLimits':'max_concurrent max_new_per_second idle_timeout_seconds',
      'PortMap':'protocol member_port guest_port',
      'AttachmentOverride':'attachment_id fork_policy',
      'Watchdog':'warn_after_ms block_after_ms repeated_action_limit on_stuck',
      'UpdatePolicy':'automatic max_unavailable channel drain_timeout_ms',
      'Compatibility':'engine_digest state_abi guest_kernel_digest cpu_baseline device_graph_digest host_os_build',
      'Outcome':'result committed_effects unknown_effects',
      'Progress':'completed total unit',
      'Effect':'resource_ref action state',
      'Destination':'hostname ip port protocol',
      'SuspendEligibility':'eligible reasons observed_at observation_state',
      'KeyEnvelope':'recipient_kind recipient_id key_id wrapping_algorithm wrapped_key_blob_id envelope_digest',
      'ReplicaReceipt':'host_id manifest_digest envelope_digest decrypt_verified_at committed_at signature',
      'CaptureProvenance':'producer_key_id producer_host_id owner_id trust_class manifest_digest engine_digest guest_kernel_digest device_graph_digest cpu_baseline signature',
      'AccelerationVariant':'capture_provenance compatibility',
      'ActionPlan':'adapter_digest operation resource_ref arguments destination secret_refs form_ref diff_blob_id redacted_fields',
      'FormRef':'origin frame_origin action_url method form_digest',
      'RevocationStatus':'enforcers complete',
      'EnforcerStatus':'host_id enforcer acknowledgment acknowledged_at authority_expires_at residual_capabilities',
      'ResidualCapability':'provider resource_ref capability_kind expires_at revoke_state',
      'ForkPreparation':'source_sandbox_id source_generation source_revision exposure_set_digest expires_at',
      'ConfigRef':'configuration_id digest',
      'SourceIdentity':'issuer repository_id repository_owner_id workflow_ref workflow_sha ref sha event environment',
      'Proof':'public_key signature',
      'JobToken':'issuer audience actions resource_ids expires_at',
      'MetadataVersions':'root targets snapshot timestamp',
      'SecurityRevocation':'digest effective_at reason metadata_version',
      'Migration':'log_version snapshot_version projection_version phase rollback_safe',
      'FenceProof':'host_id boot_id authority_epoch method stopped_at expires_at receipt',
      'CommandDetails':'input_sequence command_digest argv command_text cwd exit_code signal redacted_fields',
      'ProcessDetails':'process_id parent_process_id process_instance_id parent_process_instance_id executable executable_digest argv user runtime exit_code redacted_fields',
      'RequestDetails':'request_id method tool url route_template headers arguments body_blob_id redacted_fields approval_id status_code',
      'ConnectionDetails':'flow_id',
      'ControlDetails':'resource_ref action_digest grant_id approval_id reason outcome',
      'ProcessInstance':'boot_id pid start_sequence',
      'Runtime':'kind runtime_id container_id',
      'DeviceRef':'sandbox_id generation device_id',
      'KeyInput':'type code action modifiers',
      'MouseInput':'type x_px y_px buttons wheel_x wheel_y',
      'AudioInput':'type format sample_rate_hz channels data_base64',
      'DeviceDelivery':'device_ref sequence delivered_at',
      'StreamDescriptor':'stream_id transport url expires_at cursor max_frame_bytes max_payload_bytes media_type width_px height_px sample_rate_hz channels',
      'Frame':'stream_id sequence channel data_base64 byte_offset location_epoch',
      'MediaFrame':'stream_id sequence device_ref media_type pts_us duration_us keyframe payload data_base64',
      'ErrorDetail':'code message retryable operation_id details',
      'ErrorEnvelope':'error request_id',
      'MutationEnvelope':'operation_id resource_ids request_id replayed operation result',
    }
    for name,fields in shapes.items():
        assert set(defs[name]['properties'])==set(fields.split()),('nested fields drift',name)
        # All names must actually appear in §§11–12, rather than inventing wire fields.
        for field in fields.split():assert re.search(r'\b'+field+r'\b',spec),(name,field,'absent from spec')
    errors={}
    for line in spec.splitlines():
        if not re.match(r'\| \d{3} `',line):continue
        current=None
        for status,code in re.findall(r'(?:(\d{3}) )?`([A-Z_]+)`',line):
            current=int(status) if status else current
            errors[code]=current
    assert schema['x-lamp-errors']==errors,'HTTP error drift'
    assert set(defs['ErrorCode']['enum'])==set(errors),'error enum drift'
    events=set()
    for kind,resource in resources.items():
        if kind=='event':continue
        for action,suffix in [('create','created'),('update','updated'),('delete','deleted')]:
            if action in resource['actions']:events.add(kind+'.'+suffix)
        if kind!='audit_record':events.add(kind+'.state_changed')
    extra_events=spec.split('Additional event names are exactly ')[1].split('\n')[0]
    events.update(re.findall(r'`([^`]+)`',extra_events))
    assert set(defs['EventType']['enum'])==events,'event drift'
    def walk(value):
        if isinstance(value,dict):
            if '$ref' in value:assert value['$ref'].startswith('#/$defs/') and value['$ref'].split('/')[-1] in defs,value
            for child in value.values():walk(child)
        elif isinstance(value,list):
            for child in value:walk(child)
    walk(defs)
    # A prose or nested-shape change that our grammar cannot yet parse fails
    # closed; review it, extend checks if needed, then update this fingerprint.
    assert hashlib.sha256(spec.encode()).hexdigest()==schema['x-lamp-spec-sha256'],'spec changed: review contract and conformance coverage'
    return f'{len(resources)} resources, {len(operations)} operations/scopes, {len(errors)} errors, {len(events)} events: match architecture §§11–12'

if __name__=='__main__':print(check())
