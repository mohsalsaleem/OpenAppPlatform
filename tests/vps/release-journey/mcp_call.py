#!/usr/bin/env python3
"""Call one tool on the OAP stdio MCP server, the way an MCP client would.

Starts `oap-mcp` with the agent credential file, performs the MCP handshake,
sends one `tools/call` (or `tools/list`) and prints a JSON envelope:
{"ok": bool, "data": ..., "error": ...}. Arguments come from a JSON file or
stdin, never the command line, so identifiers and specs stay out of shell
history. The credential is read by oap-mcp itself; this script never sees it.

Usage:
  mcp_call.py --bin ./bin/oap-mcp --url URL --credential-file agent.json list
  mcp_call.py --bin ./bin/oap-mcp --url URL --credential-file agent.json call create_action_plan --args req.json
"""
import argparse
import json
import subprocess
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--bin', default='./bin/oap-mcp')
    parser.add_argument('--url', required=True)
    parser.add_argument('--credential-file', required=True)
    parser.add_argument('--read-only', action='store_true', help='omit --operate (default stdio lifecycle tools on)')
    parser.add_argument('--timeout', type=float, default=1900.0, help='seconds; wait_operation may block up to 1800')
    parser.add_argument('mode', choices=['list', 'call'])
    parser.add_argument('tool', nargs='?')
    parser.add_argument('--args', help='JSON file with tool arguments; "-" for stdin; default {}')
    a = parser.parse_args()
    if a.mode == 'call' and not a.tool:
        parser.error('call requires a tool name')

    arguments = {}
    if a.args == '-':
        arguments = json.load(sys.stdin)
    elif a.args:
        with open(a.args) as handle:
            arguments = json.load(handle)

    command = [a.bin, '-url', a.url, '-credential-file', a.credential_file]
    if not a.read_only:
        command.append('-operate')
    messages = [
        {'jsonrpc': '2.0', 'id': 1, 'method': 'initialize',
         'params': {'protocolVersion': '2024-11-05', 'capabilities': {},
                    'clientInfo': {'name': 'oap-release-journey', 'version': '1'}}},
        {'jsonrpc': '2.0', 'method': 'notifications/initialized'},
        {'jsonrpc': '2.0', 'id': 2, 'method': 'tools/list'} if a.mode == 'list' else
        {'jsonrpc': '2.0', 'id': 2, 'method': 'tools/call', 'params': {'name': a.tool, 'arguments': arguments}},
    ]
    stdin = ''.join(json.dumps(m) + '\n' for m in messages)
    try:
        process = subprocess.run(command, input=stdin, capture_output=True, text=True, timeout=a.timeout)
    except subprocess.TimeoutExpired:
        print(json.dumps({'ok': False, 'error': {'code': 'client_timeout'}}))
        return 7

    response = None
    for line in process.stdout.splitlines():
        try:
            message = json.loads(line)
        except ValueError:
            continue
        if message.get('id') == 2:
            response = message
    if response is None:
        print(json.dumps({'ok': False, 'error': {'code': 'no_response', 'stderr': process.stderr.strip()[-500:]}}))
        return 5
    if 'error' in response:
        print(json.dumps({'ok': False, 'error': response['error']}))
        return 4
    result = response.get('result', {})
    if a.mode == 'list':
        print(json.dumps({'ok': True, 'data': [t['name'] for t in result.get('tools', [])]}))
        return 0
    structured = result.get('structuredContent')
    if structured is None:
        text = (result.get('content') or [{}])[0].get('text', '')
        try:
            data = json.loads(text)
        except ValueError:
            data = text
        structured = {'ok': not result.get('isError'), 'data' if not result.get('isError') else 'error': data}
    print(json.dumps(structured, indent=2))
    return 0 if structured.get('ok') else 3


if __name__ == '__main__':
    sys.exit(main())
