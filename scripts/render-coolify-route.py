#!/usr/bin/env python3
"""Render a native Traefik file-provider route; never apply it or alter Coolify."""
import argparse
import json
import re
from urllib.parse import urlsplit


def render(name, domain, upstreams, health_path='/health/ready'):
    if not re.fullmatch(r'oap-[a-z0-9-]{1,48}', name):
        raise ValueError('Route name must be an oap-prefixed slug')
    if not re.fullmatch(r'[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?', domain) or '.' not in domain or '..' in domain:
        raise ValueError('Domain must be an explicit hostname')
    if not 1 <= len(upstreams) <= 4 or len(set(upstreams)) != len(upstreams):
        raise ValueError('Provide one to four unique stable upstream URLs')
    for endpoint in upstreams:
        parsed = urlsplit(endpoint)
        if parsed.scheme not in ('http', 'https') or not parsed.hostname or parsed.username or parsed.password or parsed.path not in ('', '/') or parsed.query or parsed.fragment or any(c.isspace() for c in endpoint):
            raise ValueError('Upstream must be an HTTP(S) origin without credentials')
        _ = parsed.port
        if parsed.hostname == domain:
            raise ValueError('Shared route cannot proxy to itself')
    if not re.fullmatch(r'/[A-Za-z0-9/_-]*', health_path):
        raise ValueError('Provide a simple absolute readiness path')
    return {'http': {'middlewares': {name + '-redirect': {'redirectScheme': {'scheme': 'https'}}},
                     'routers': {name + '-http': {'rule': 'Host(`' + domain + '`)', 'entryPoints': ['http'], 'middlewares': [name + '-redirect'], 'service': name},
                                 name + '-https': {'rule': 'Host(`' + domain + '`)', 'entryPoints': ['https'], 'service': name, 'tls': {'certResolver': 'letsencrypt'}}},
                     'services': {name: {'loadBalancer': {'passHostHeader': False, 'healthCheck': {'path': health_path, 'interval': '2s', 'timeout': '1s'}, 'servers': [{'url': url.rstrip('/')} for url in upstreams]}}}}}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--name', required=True)
    parser.add_argument('--domain', required=True)
    parser.add_argument('--upstream', action='append', required=True)
    parser.add_argument('--health-path', default='/health/ready')
    args = parser.parse_args()
    try:
        print(json.dumps(render(args.name, args.domain, args.upstream, args.health_path), indent=2))
    except ValueError as error:
        parser.error(str(error))
