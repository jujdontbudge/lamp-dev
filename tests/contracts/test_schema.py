import copy
import importlib.util
import json
from pathlib import Path
import unittest
from jsonschema import Draft202012Validator, FormatChecker, ValidationError

ROOT=Path(__file__).resolve().parents[2]
SCHEMA=json.loads((ROOT/'contracts/public/schema.json').read_text())
loader=importlib.util.spec_from_file_location('check_contracts',ROOT/'tools/check_contracts.py')
conformance=importlib.util.module_from_spec(loader);loader.loader.exec_module(conformance)


def validate(name,value):
    Draft202012Validator({'$ref':'#/$defs/'+name,'$defs':SCHEMA['$defs']},format_checker=FormatChecker()).validate(value)


class Contracts(unittest.TestCase):
    def test_all_schemas_well_formed(self):
        Draft202012Validator.check_schema(SCHEMA)
        for name,s in SCHEMA['$defs'].items():
            Draft202012Validator.check_schema(s)
        self.assertTrue(conformance.check())

    def test_drift_checks_fail_closed(self):
        mutations=[lambda s:s['x-lamp-resources'].pop('sandbox'),
                   lambda s:s['$defs']['Sandbox']['properties'].pop('network_plans'),
                   lambda s:s['$defs']['ActionPlan']['properties'].pop('adapter_digest'),
                   lambda s:s['x-lamp-operations'].pop('session_spawn'),
                   lambda s:s['x-lamp-operations']['sandbox_file_read'].update(scope='sandbox.get'),
                   lambda s:s['x-lamp-errors'].update(NOT_FOUND=403),
                   lambda s:s['$defs']['EventType']['enum'].remove('exposure.activated')]
        for mutate in mutations:
            schema=copy.deepcopy(SCHEMA);mutate(schema)
            with self.assertRaises((AssertionError,KeyError)):conformance.check(schema)
        spec=(ROOT/'docs/spec/architecture.md').read_text().replace('`input` is a tagged object','`input` is an altered tagged object')
        with self.assertRaises(AssertionError):conformance.check(architecture=spec)

    def test_reviewed_resource_and_operation_drift(self):
        def move_deadline(schema):
            defs = schema['$defs']
            defs['HostBenchInput']['properties']['deadline_ms'] = defs['HostDrainInput']['properties'].pop('deadline_ms')
        mutations = {
            'missing common resource field': lambda s: s['$defs']['Sandbox']['properties'].pop('owner_id'),
            'invented resource field': lambda s: s['$defs']['Sandbox']['properties'].update(invented_authority={'type':'string'}),
            'input moved between actions': move_deadline,
            'wrong extra route': lambda s: s['x-lamp-operations']['sandbox_resume'].update(path='/v1/wrong'),
            'wrong extra method': lambda s: s['x-lamp-operations']['sandbox_resume'].update(method='get'),
            'missing result field': lambda s: s['$defs']['SandboxPrepareForkCompletion']['properties'].pop('generation'),
            'invented input field': lambda s: s['$defs']['HostDrainInput']['properties'].update(invented={'type':'string'}),
            'invented result field': lambda s: s['$defs']['SandboxPrepareForkCompletion']['properties'].update(invented={'type':'string'}),
        }
        for label, mutate in mutations.items():
            with self.subTest(label=label):
                schema = copy.deepcopy(SCHEMA)
                mutate(schema)
                with self.assertRaises(AssertionError):
                    conformance.check(schema)

    def test_create_and_unknown_fields(self):
        good={'template_id':'tp_fixture','idempotency_key':'lk1.1.hs.fixture'}
        validate('SandboxCreateInput',good)
        for invalid in [{},dict(good,recipe_id='rc_fixture'),dict(good,running=True),dict(good,state='ready'),dict(good,host_path='/tmp/private')]:
            with self.assertRaises(ValidationError):validate('SandboxCreateInput',invalid)

    def test_uint64_precision_and_scope_empty(self):
        validate('Scope',{'sandbox_ids':[]})
        validate('Scope',{})
        with self.assertRaises(ValidationError):validate('Scope',{'actions':['sandbox.running']})
        base={'session_id':'se_fixture','lease_id':'le_fixture','location_epoch':2**64-1,'sequence':2**64-1,'data_base64':'8J+SqQ==','expected_revision':2**64-1,'idempotency_key':'key'}
        validate('SessionInputInput',base)
        for bad in [-1,2**64,1.5]:
            with self.assertRaises(ValidationError):validate('SessionInputInput',dict(base,sequence=bad))

    def test_tags_enums_and_location(self):
        validate('DeviceInput',{'type':'key','code':4,'action':'down','modifiers':['shift']})
        validate('DeviceInput',{'type':'mouse','x_px':0,'y_px':1,'buttons':[],'wheel_x':-1,'wheel_y':0})
        validate('DeviceInput',{'type':'audio','format':'pcm_s16le','sample_rate_hz':48000,'channels':1,'data_base64':'AAA='})
        for bad in [{'type':'usb'},{'type':'key','code':65536,'action':'down','modifiers':[]},{'type':'mouse','x_px':-1,'y_px':0,'buttons':[],'wheel_x':0,'wheel_y':0}]:
            with self.assertRaises(ValidationError):validate('DeviceInput',bad)
        validate('Location',{'kind':'host','host_id':'hs_fixture'})
        validate('Location',{'kind':'sandbox','host_id':'hs_fixture','sandbox_id':'sb_fixture'})
        for bad in [{'kind':'sandbox','host_id':'hs_fixture'},{'kind':'host','host_id':'hs_fixture','sandbox_id':'sb_fixture'}]:
            with self.assertRaises(ValidationError):validate('Location',bad)

    def test_expiry_continuation_and_preauth_shape(self):
        base={'sandbox_id':'sb_fixture','expected_revision':7,'idempotency_key':'key'}
        validate('SandboxResumeInput',base)
        validate('SandboxResumeInput',dict(base,continuation_task_id='tk_new',expected_task_id='tk_old'))
        with self.assertRaises(ValidationError):validate('SandboxResumeInput',dict(base,continuation_task_id='tk_new'))
        boot={'fleet_id':'fl_fixture','purpose':'ci_exchange','proof_key_thumbprint':'hash','source_identity':{'issuer':'forge'}}
        validate('AuthFlowBootstrapInput',boot)
        for bad in [dict(boot,purpose='provider_login'),dict(boot,idempotency_key='key')]:
            with self.assertRaises(ValidationError):validate('AuthFlowBootstrapInput',bad)
        with self.assertRaises(ValidationError):validate('AuthFlowBootstrapInput',{k:v for k,v in boot.items() if k!='source_identity'})

    def test_nested_readiness_and_transfer(self):
        value={'eligible':True,'reasons':[],'observed_at':'2026-11-09T00:00:00Z','observation_state':'fresh'}
        validate('SuspendEligibility',value)
        with self.assertRaises(ValidationError):validate('SuspendEligibility',dict(value,observation_state='unknown'))
        base={'sandbox_id':'sb_fixture','path':'/work/a','direction':'upload','blob_id':'bl_fixture','expected_revision':1,'idempotency_key':'key'}
        validate('SandboxFileTransferInput',base)
        with self.assertRaises(ValidationError):validate('SandboxFileTransferInput',dict(base,direction='download'))

    def test_transient_results_and_wrapped_reads(self):
        validate('KeyCreateCompletion',{'key_id':'ky_fixture','secret':'once'})
        validate('KeyCreateCompletion',{'key_id':'ky_fixture','secret_available':False})
        self.assertNotIn('secret',SCHEMA['$defs']['Operation']['properties'])
        self.assertNotIn('secret',SCHEMA['$defs']['Event']['properties'])
        validate('SessionAttachCompletion',{'stream':{'stream_id':'st_fixture','transport':'websocket','url':'wss://host/v1/sessions/se/output','expires_at':'2026-11-09T00:00:00Z','max_frame_bytes':69632,'max_payload_bytes':65536}})
        self.assertEqual(set(SCHEMA['$defs']['SandboxGetResult']['required']),{'data','request_id'})

    def test_all_operation_inputs_are_closed(self):
        for name,o in SCHEMA['x-lamp-operations'].items():
            inp=SCHEMA['$defs'][o['input_schema']]
            self.assertFalse(inp['additionalProperties'],name)
            # Deliberately inject a field no operation may accept.
            with self.assertRaises(ValidationError):validate(o['input_schema'],{'__untrusted_host_path':'/tmp'})

if __name__=='__main__':unittest.main()
