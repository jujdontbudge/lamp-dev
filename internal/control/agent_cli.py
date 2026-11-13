#!/usr/bin/env python3
"""Guest CLI/MCP for a session's endpoint-scoped Lamp capability."""
import json
import base64
import os
from pathlib import Path
import sys
import urllib.request
import urllib.error
import uuid


def call(method, path, body=None, revision=None):
    base = os.environ['LAMP_API_URL']
    token = Path(os.environ['LAMP_TOKEN_FILE']).read_text().strip()
    headers = {'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'}
    if method != 'GET':
        headers['Idempotency-Key'] = uuid.uuid4().hex
    if revision is not None:
        headers['If-Match'] = str(revision)
    request = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), method=method, headers=headers)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPRedirectHandler())
    try:
        with opener.open(request, timeout=65) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        return json.load(error)


def invoke(connection, operation, arguments):
    return call('POST', '/v1/invoke', dict(connection_id=connection, operation=operation, arguments=arguments))


def mutate(identity, action, body=None):
    current = call('GET', '/v1/sessions/' + identity)
    if 'error' in current: return current
    return call('POST', '/v1/sessions/' + identity + ':' + action, body or {}, current['data']['revision'])


def workflow(action, args):
    own = os.environ['LAMP_SESSION_ID']
    if action == 'spawn':
        args = dict(args)
        child = None
        if args.get('sandbox_mode') == 'new':
            created = call('POST', '/v1/sandboxes', {'wait_ms': 60000})
            if 'error' in created: return created
            child = next((ref['id'] for ref in created.get('resource_ids', []) if ref['kind'] == 'sandbox'), None)
            if not child: return created
            args['sandbox_mode'] = 'sandbox:' + child
        result = mutate(own, 'spawn', dict(args, wait_ms=60000))
        if 'error' in result and child:
            state = call('GET', '/v1/sandboxes/' + child)
            if 'data' in state: call('DELETE', '/v1/sandboxes/' + child, {'wait_ms': 60000}, state['data']['revision'])
        return result
    if action in ('message', 'cancel'):
        args = dict(args)
        identity = args.pop('session_id')
        return mutate(identity, action, args)
    if action == 'wait':
        identity = args['session_id']
        state = call('GET', '/v1/sessions/' + identity)
        if 'error' in state: return state
        output = call('GET', '/v1/sessions/' + identity + '/output?wait_ms=1000')
        return dict(session=state['data'], output=output)
    if action == 'git_return':
        parent = call('GET', '/v1/sessions/' + own)['data'].get('parent_session_id')
        if not parent: raise ValueError('session has no parent')
        return call('POST', '/v1/change-sets', dict(source_session_id=own, target_session_id=parent, wait_ms=60000))
    raise ValueError('unknown workflow action')


def mcp():
    tools = json.loads(os.environ.get('LAMP_MCP_TOOLS', '[]'))
    for line in sys.stdin:
        request = json.loads(line)
        if 'id' not in request:
            continue
        method = request.get('method')
        if method == 'initialize':
            result = {'protocolVersion': '2025-06-18', 'capabilities': {'tools': {}}, 'serverInfo': {'name': 'lamp', 'version': '1'}}
        elif method == 'tools/list':
            result = {'tools': tools}
        elif method == 'tools/call':
            params = request['params']; args = params.get('arguments', {})
            tool = next(t for t in tools if t['name'] == params['name'])
            value = workflow(tool['_meta']['lamp.workflow'], args) if 'lamp.workflow' in tool['_meta'] else invoke(tool['_meta']['lamp.connection_id'], tool['_meta']['lamp.operation'], args)
            result = {'content': [{'type': 'text', 'text': json.dumps(value)}], 'isError': 'error' in value}
        else:
            print(json.dumps({'jsonrpc': '2.0', 'id': request['id'], 'error': {'code': -32601, 'message': 'Unknown method'}}), flush=True)
            continue
        print(json.dumps({'jsonrpc': '2.0', 'id': request['id'], 'result': result}), flush=True)


def main():
    args = [a for a in sys.argv[1:] if a != '--json']
    if args == ['mcp']:
        mcp(); return
    if len(args) < 2:
        raise ValueError('usage: lamp sandbox create|get|exec|delete; lamp connection invoke; lamp settings-source copy|propose')
    kind, action, *args = args
    if kind == 'session':
        if action in ('spawn', 'side'):
            import argparse
            parser = argparse.ArgumentParser(description='Children copy files; side chats share files and branch, with a fresh conversation.')
            parser.add_argument('--prompt', required=True)
            parser.add_argument('--sandbox-mode', default='same')
            parsed = parser.parse_args(args)
            value = workflow('spawn', dict(prompt=parsed.prompt, sandbox_mode=parsed.sandbox_mode, side=action == 'side'))
        elif action == 'message':
            value = workflow('message', dict(session_id=args[0], text=' '.join(args[1:])))
        elif action in ('wait', 'cancel'):
            value = workflow(action, dict(session_id=args[0]))
        else: raise ValueError('unsupported session action')
    elif kind == 'change-set':
        if action == 'return':
            value = workflow('git_return', {})
        elif action == 'apply':
            current = call('GET', '/v1/change-sets/' + args[0])
            value = current if 'error' in current else call('POST', '/v1/change-sets/' + args[0] + ':apply', {'wait_ms':60000}, current['data']['revision'])
        else: raise ValueError('unsupported change-set action')
    elif kind == 'sandbox':
        if action == 'create':
            body = {'wait_ms': 60000}
            if args:
                if len(args) != 2 or args[0] != '--wait-ms':
                    raise ValueError('nested create inherits parent settings; only --wait-ms is accepted')
                body['wait_ms'] = int(args[1])
            value = call('POST', '/v1/sandboxes', body)
        elif action == 'get':
            value = call('GET', '/v1/sandboxes/' + args[0])
        elif action == 'delete':
            revision = call('GET', '/v1/sandboxes/' + args[0])['data']['revision']
            value = call('DELETE', '/v1/sandboxes/' + args[0], {}, revision)
        elif action == 'exec':
            child, *argv = args
            if argv and argv[0] == '--': argv = argv[1:]
            value = call('POST', '/v1/execs', {'sandbox_id': child, 'argv': argv, 'wait_ms': 60000})
        else:
            raise ValueError('nested sandbox action not permitted')
    elif kind == 'secret' and action in ('use', 'request'):
        body = json.load(sys.stdin)
        value = call('POST', '/v1/secrets/use' if action == 'use' else '/v1/secrets:request', body)
    elif kind == 'connection' and action == 'invoke':
        connection, operation, *rest = args
        arguments = json.load(sys.stdin) if rest == ['--input', '-'] else {}
        value = invoke(connection, operation, arguments)
    elif kind == 'settings-source' and action in ('copy', 'propose'):
        source, *rest = args
        body = json.load(sys.stdin) if action == 'propose' else {}
        value = call('POST', '/v1/settings/' + source + '/' + action, body)
    else:
        raise ValueError('operation outside guest capability')
    print(json.dumps(value))
    if 'error' in value: sys.exit(1)


if __name__ == '__main__':
    main()
