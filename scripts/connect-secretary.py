#!/usr/bin/env python3
"""Interactive, read-only MCP OAuth enrollment. Secrets stay outside the repo."""
import argparse
import base64
import hashlib
import hmac
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import os
from pathlib import Path
import secrets
import ssl
import tempfile
import time
from urllib.parse import urlencode, urlsplit, parse_qs
from urllib.request import Request, build_opener, HTTPSHandler, HTTPRedirectHandler

PROVIDERS = {
    "granola": {"resource": "https://mcp.granola.ai/mcp",
                "metadata": "https://mcp.granola.ai/.well-known/oauth-protected-resource",
                "auth_host": "mcp-auth.granola.ai", "scope": "mcp"},
}


def metadata_url(issuer):
    u = urlsplit(issuer)
    return f"{u.scheme}://{u.netloc}/.well-known/oauth-authorization-server{u.path.rstrip('/')}"


def validate_endpoint(provider, url):
    u = urlsplit(url)
    if u.scheme != "https" or u.hostname != PROVIDERS[provider]["auth_host"] or u.port not in (None, 443) or u.username or u.password or u.fragment:
        raise ValueError("OAuth endpoint outside the provider allowlist")
    return url


def callback_code(query, expected):
    state = query.get("state", [""])
    code = query.get("code", [""])
    if len(state) != 1 or len(code) != 1 or not hmac.compare_digest(state[0], expected) or not code[0] or "error" in query:
        raise ValueError("OAuth callback rejected")
    return code[0]


def callback_uri(provider, port, public_origin=None):
    if provider not in PROVIDERS:
        raise ValueError('Unknown OAuth provider')
    if public_origin is None:
        return f'http://127.0.0.1:{port}/callback'
    if public_origin != 'https://assistant.stratecode.com':
        raise ValueError('Public OAuth origin must be the configured HTTPS assistant domain')
    return f'{public_origin}/oauth/{provider}/callback'


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        raise ValueError("OAuth HTTP redirect rejected")


def request_json(url, payload=None, form=False):
    ctx = ssl.create_default_context()
    if Path('/etc/ssl/cert.pem').exists():
        ctx.load_verify_locations('/etc/ssl/cert.pem')
    headers = {"Accept": "application/json"}
    data = None
    if payload is not None:
        headers['Content-Type'] = 'application/x-www-form-urlencoded' if form else 'application/json'
        data = (urlencode(payload) if form else json.dumps(payload)).encode()
    opener = build_opener(HTTPSHandler(context=ctx), NoRedirect())
    with opener.open(Request(url, data=data, headers=headers), timeout=30) as response:
        body = response.read(1024 * 1024 + 1)
    if len(body) > 1024 * 1024:
        raise ValueError('OAuth response too large')
    return json.loads(body)


def discover(provider):
    cfg = PROVIDERS[provider]
    resource = request_json(cfg['metadata'])
    if resource.get('resource') != cfg['resource'] or len(resource.get('authorization_servers', [])) != 1:
        raise ValueError('Unexpected protected resource metadata')
    issuer = validate_endpoint(provider, resource['authorization_servers'][0])
    auth = request_json(metadata_url(issuer))
    if auth.get('issuer') != issuer or 'S256' not in auth.get('code_challenge_methods_supported', []):
        raise ValueError('OAuth issuer or PKCE unsupported')
    for field in ('authorization_endpoint', 'token_endpoint', 'registration_endpoint'):
        validate_endpoint(provider, auth[field])
    return auth


def save_config(path, provider, value):
    path = path.expanduser().resolve()
    repository = Path(__file__).resolve().parents[1]
    if path == repository or repository in path.parents:
        raise ValueError('OAuth credentials must be stored outside the repository')
    path.parent.mkdir(parents=True, mode=0o700, exist_ok=True)
    config = json.loads(path.read_text()) if path.exists() else {}
    config[provider] = value
    fd, tmp = tempfile.mkstemp(prefix='.secretary-', dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as handle:
            json.dump(config, handle, indent=2)
            handle.write('\n')
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(tmp, path)
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)


def connect(provider, path, port, public_origin=None):
    auth = discover(provider)
    redirect = callback_uri(provider, port, public_origin)
    state, verifier = secrets.token_urlsafe(32), secrets.token_urlsafe(48)
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b'=').decode()
    received = {}

    class Callback(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_GET(self):
            u = urlsplit(self.path)
            try:
                if u.path != '/callback':
                    raise ValueError('Unexpected callback path')
                received['code'] = callback_code(parse_qs(u.query), state)
                body, status = b'Authorization received. You can close this tab.', 200
            except ValueError:
                body, status = b'Authorization rejected. Retry from the original link.', 400
            self.send_response(status)
            self.send_header('Content-Type', 'text/plain; charset=utf-8')
            self.send_header('Cache-Control', 'no-store')
            self.end_headers()
            self.wfile.write(body)

    with HTTPServer(('127.0.0.1', port), Callback) as server:
        client = request_json(auth['registration_endpoint'], {
            'client_name': 'StrateCode Lab Secretary', 'redirect_uris': [redirect],
            'grant_types': ['authorization_code', 'refresh_token'], 'response_types': ['code'],
            'token_endpoint_auth_method': 'none',
        })
        if not client.get('client_id'):
            raise ValueError('OAuth registration did not return a client ID')
        query = {'response_type': 'code', 'client_id': client['client_id'], 'redirect_uri': redirect,
                 'scope': PROVIDERS[provider]['scope'], 'state': state, 'code_challenge': challenge,
                 'code_challenge_method': 'S256', 'resource': PROVIDERS[provider]['resource']}
        print('AUTHORIZE_URL=' + auth['authorization_endpoint'] + '?' + urlencode(query), flush=True)
        server.timeout = 1
        deadline = time.monotonic() + 1200
        while not received and time.monotonic() < deadline:
            server.handle_request()
        if not received:
            raise ValueError('OAuth authorization timed out')
    payload = {'grant_type': 'authorization_code', 'code': received['code'], 'redirect_uri': redirect,
               'client_id': client['client_id'], 'code_verifier': verifier,
               'resource': PROVIDERS[provider]['resource']}
    if client.get('client_secret'):
        payload['client_secret'] = client['client_secret']
    token = request_json(auth['token_endpoint'], payload, form=True)
    if not token.get('access_token') or token.get('token_type', 'Bearer').lower() != 'bearer':
        raise ValueError('OAuth token response rejected')
    value = {'server_url': PROVIDERS[provider]['resource'], 'access_token': token['access_token'],
             'refresh_token': token.get('refresh_token', ''), 'client_id': client['client_id'],
             'client_secret': client.get('client_secret', ''), 'token_endpoint': auth['token_endpoint'],
             'expires_at': int(time.time()) + int(token.get('expires_in', 3600))}
    save_config(path, provider, value)
    print(f'{provider}: OAuth saved privately to {path.expanduser()}', flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('provider', choices=PROVIDERS)
    parser.add_argument('--discover', action='store_true')
    parser.add_argument('--output', type=Path, default=Path('~/.config/stratecode-lab/secretary-connectors.json'))
    parser.add_argument('--port', type=int, default=18765)
    parser.add_argument('--public-origin', default=None)
    args = parser.parse_args()
    try:
        if args.discover:
            auth = discover(args.provider)
            print(json.dumps({k: auth[k] for k in ('issuer', 'authorization_endpoint', 'token_endpoint', 'registration_endpoint')}, indent=2))
        else:
            connect(args.provider, args.output, args.port, args.public_origin)
    except KeyboardInterrupt:
        raise SystemExit('OAuth cancelled')
    except Exception as error:
        # Never print HTTP request bodies, token URLs, authorization codes or provider error payloads.
        raise SystemExit(f'OAuth failed ({type(error).__name__}); credentials were not printed')


if __name__ == '__main__':
    main()
