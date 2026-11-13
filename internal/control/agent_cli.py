#!/usr/bin/env python3
"""Guest CLI/MCP for a session's endpoint-scoped Lamp capability."""
import json
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
            value = invoke(tool['_meta']['lamp.connection_id'], tool['_meta']['lamp.operation'], args)
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
    if kind == 'sandbox':
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
