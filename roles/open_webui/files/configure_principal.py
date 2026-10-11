"""Reconcile managed model settings through Open WebUI's authenticated API.

Runs inside the existing container. A short-lived local JWT is never emitted.
Other model metadata/parameters and all chats remain intact.
"""
import json
import os
import sqlite3
import time
import urllib.error
import urllib.request

import jwt


def main():
    db = sqlite3.connect('file:/app/backend/data/webui.db?mode=ro', uri=True)
    owner = db.execute("SELECT id FROM user WHERE role='admin' LIMIT 1").fetchone()
    if not owner:
        print(json.dumps({'changed': False, 'configured': False,
                          'reason': 'Create the first administrator, then rerun deployment.'}))
        return
    token = jwt.encode({'id': owner[0], 'exp': int(time.time()) + 120},
                       os.environ['WEBUI_SECRET_KEY'], algorithm='HS256')
    base = 'http://127.0.0.1:' + os.environ.get('PORT', '3080')
    headers = {'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'}

    def request(path, body=None):
        req = urllib.request.Request(base + path, headers=headers,
            data=json.dumps(body).encode() if body is not None else None)
        with urllib.request.urlopen(req, timeout=30) as response:
            return json.load(response)

    try:
        existing = request('/api/v1/models/model?id=main')
    except urllib.error.HTTPError as error:
        if error.code not in (401, 404):
            raise
        existing = None
    existing = existing or {}
    meta = dict(existing.get('meta') or {})
    capabilities = dict(meta.get('capabilities') or {})
    capabilities.update(builtin_tools=False, vision=False)
    meta['capabilities'] = capabilities
    params = dict(existing.get('params') or {})
    params.update(stream_response=True, max_tokens=512, temperature=0)
    payload = {'id': 'main', 'base_model_id': None,
        'name': os.environ.get('PRINCIPAL_MODEL_NAME', 'LFM2.5 1.2B · Principal'),
        'meta': meta, 'params': params, 'is_active': True,
        'access_grants': existing.get('access_grants') or []}
    changed = not existing or any(existing.get(k) != payload[k]
        for k in ('base_model_id', 'name', 'meta', 'params', 'is_active'))
    if changed:
        request('/api/v1/models/model/update' if existing else '/api/v1/models/create', payload)
    effective = request('/api/models')['data']
    principal = next(m for m in effective if m['id'] == 'main')
    info = principal.get('info') or {}
    stored = request('/api/v1/models/model?id=main')
    assert stored['params']['stream_response'] is True
    settings = request('/api/v1/users/user/settings') or {}
    ui = dict(settings.get('ui') or {})
    user_params = dict(ui.get('params') or {})
    if user_params.get('stream_response') is not True:
        user_params['stream_response'] = True
        ui['params'] = user_params
        settings['ui'] = ui
        request('/api/v1/users/user/settings/update', settings)
        changed = True
    effective_settings = request('/api/v1/users/user/settings')
    assert effective_settings['ui']['params']['stream_response'] is True
    assert info['meta']['capabilities']['builtin_tools'] is False
    print(json.dumps({'changed': changed, 'configured': True,
                      'model': principal['id'], 'stream_response': True,
                      'builtin_tools': False}))


if __name__ == '__main__':
    main()
