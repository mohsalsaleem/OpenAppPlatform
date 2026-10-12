#!/usr/bin/env python3
"""Reject undocumented HTTP routes, ambiguous paths and competing UI primitives."""
import json
from pathlib import Path
import re
ROOT=Path(__file__).resolve().parent.parent
spec=json.loads((ROOT/'docs/openapi.json').read_text())
schemas=spec['components']['schemas']
if set(schemas['Component']['properties']) != set(schemas['ComponentInput']['properties']) or schemas['Component']['properties']['kind']['enum'] != schemas['ComponentInput']['properties']['kind']['enum']:
    raise SystemExit('Component request/response fields or kinds disagree')
for path,operations in spec['paths'].items():
    names=re.findall(r'\{([^}]+)\}',path)
    if len(names)!=len(set(names)):
        raise SystemExit('Duplicate path parameter: '+path)
    for method,operation in operations.items():
        if method not in {'get','post','put','patch','delete','head','options','trace'}:
            continue
        params={p['name'] for p in operation.get('parameters',[]) if p['in']=='path'}
        if set(names)!=params:
            raise SystemExit('Path parameters disagree with route: '+method+' '+path)
for source in (ROOT/'internal/httpapi').glob('*.go'):
    if source.name.endswith('_test.go'):
        continue
    for method,path in re.findall(r'HandleFunc\("(GET|POST|PUT|PATCH|DELETE) (/api/v1/[^" ]+)"',source.read_text()):
        if method.lower() not in spec['paths'].get(path,{}):
            raise SystemExit('Undocumented HTTP route: '+method+' '+path)
for source in (ROOT/'web/src').rglob('*.tsx'):
    if 'components/ui' in source.as_posix():
        continue
    if re.search(r'<(?:button|input|select|textarea|table|dialog|fieldset|legend|details|summary)\b',source.read_text()):
        raise SystemExit('Use a shared shadcn primitive: '+str(source.relative_to(ROOT)))
css=(ROOT/'web/src/styles.css').read_text()
if re.search(r'(?m)^(?:label|summary|\.button|a\.button(?:\.primary|\.secondary)?)\s*\{',css):
    raise SystemExit('Legacy global control styles compete with shared UI primitives')
print('HTTP and UI contracts agree')
