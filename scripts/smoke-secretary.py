#!/usr/bin/env python3
"""Bounded probe for the shared secretary. Prints synthetic content and timings only."""
import json
import os
import time
from urllib.error import HTTPError
from urllib.request import Request, urlopen

base = os.environ.get('LAB_SECRETARY_BASE_URL', 'http://127.0.0.1:8082').rstrip('/')
key = os.environ['LAB_SECRETARY_MODEL_KEY']

def call(path, body=None, auth=True):
    headers = {'Content-Type': 'application/json'}
    if auth:
        headers['Authorization'] = 'Bearer ' + key
    return urlopen(Request(base + path, data=json.dumps(body).encode() if body is not None else None, headers=headers), timeout=120)

with call('/health', auth=False) as response:
    assert json.load(response)['status'] is True
try:
    call('/v1/models', auth=False)
    raise AssertionError('unauthenticated access accepted')
except HTTPError as error:
    assert error.code == 401
with call('/v1/models') as response:
    models = json.load(response)['data']
    assert len(models) == 1 and models[0]['id'] == 'main'
# Status avoids exposing real source documents in evidence.
with call('/v1/chat/completions', {'model': 'main', 'stream': True, 'messages': [{'role': 'user', 'content': '/status'}]}) as response:
    status = response.read().decode()
    assert 'Jira:' in status and 'Granola:' in status and '[DONE]' in status
    assert 'finish_reason' in status
start = time.monotonic()
first = None
parts = []
done = False
with call('/v1/chat/completions', {'model': 'main', 'stream': True, 'temperature': 0, 'max_tokens': 32, 'messages': [{'role': 'user', 'content': 'Saluda en una frase breve en español.'}]}) as response:
    assert response.headers.get('Content-Type', '').startswith('text/event-stream')
    for line in response:
        if not line.startswith(b'data:'):
            continue
        data = line[5:].strip()
        if data == b'[DONE]':
            done = True
            break
        event = json.loads(data)
        choices = event.get('choices', [])
        content = choices[0].get('delta', {}).get('content', '') if choices else ''
        if content:
            if first is None:
                first = time.monotonic() - start
            parts.append(content)
assert done and parts and first is not None
print(json.dumps({'health': True, 'unauthenticated_http': 401, 'models': ['main'], 'shared_status_stream': True,
                  'stream_done': done, 'first_token_seconds': round(first, 3),
                  'duration_seconds': round(time.monotonic() - start, 3), 'synthetic_answer': ''.join(parts)}, ensure_ascii=False, indent=2))
