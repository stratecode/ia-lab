#!/usr/bin/env python3
"""Read-only principal-model smoke: chat, stream, tools and optional >8k context.

LAB_MODEL_API_URL defaults to host loopback; LAB_MODEL_API_KEY is optional.
No tools are executed. Output contains only synthetic probes, never API keys.
"""
import argparse
import json
import os
import time
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--long-context', action='store_true')
    parser.add_argument('--output')
    args = parser.parse_args()
    base = os.environ.get('LAB_MODEL_API_URL', 'http://127.0.0.1:8080').rstrip('/')
    headers = {'Content-Type': 'application/json'}
    key = os.environ.get('LAB_MODEL_API_KEY')
    if key:
        headers['Authorization'] = 'Bearer ' + key

    def request(path, data=None):
        req = urllib.request.Request(base + path, headers=headers,
            data=json.dumps(data).encode() if data is not None else None)
        return urllib.request.urlopen(req, timeout=600)

    def post(path, data):
        with request(path, data) as response:
            return json.load(response)

    with request('/health') as response:
        assert json.load(response)['status'] == 'ok'
    with request('/v1/models') as response:
        models = json.load(response)['data']
    assert [m['id'] for m in models] == ['main'], models
    messages = [{'role': 'user', 'content': 'La reunión del proyecto Atlas es el martes a las 10. Responde OK.'},
                {'role': 'assistant', 'content': 'OK.'},
                {'role': 'user', 'content': '¿Qué día y a qué hora es la reunión? Responde en español.'}]
    start = time.monotonic()
    chat = post('/v1/chat/completions', {'model': 'main', 'messages': messages,
        'temperature': 0, 'max_tokens': 96})
    text = chat['choices'][0]['message']['content']
    assert 'martes' in text.lower() and '10' in text, text
    result = {'chat_elapsed_s': round(time.monotonic() - start, 3), 'chat': text,
              'usage': chat.get('usage'), 'timings': chat.get('timings')}
    with request('/v1/chat/completions', {'model': 'main', 'messages': [
            {'role': 'user', 'content': 'Responde solamente: Hola.'}],
            'temperature': 0, 'max_tokens': 24, 'stream': True}) as response:
        chunks = []
        for line in response:
            if line.startswith(b'data: ') and line.strip() != b'data: [DONE]':
                data = json.loads(line[6:])
                if data.get('choices'):
                    chunks.append(data['choices'][0]['delta'].get('content') or '')
    assert ''.join(chunks).strip(), chunks
    result['stream'] = ''.join(chunks)
    tools = [{'type': 'function', 'function': {'name': 'service_status',
        'description': 'Consulta estado sin modificar el servicio', 'parameters': {
            'type': 'object', 'properties': {'unit': {'type': 'string', 'enum': ['llama-cpp-main']}},
            'required': ['unit'], 'additionalProperties': False}}}]
    response = post('/v1/chat/completions', {'model': 'main', 'messages': [
        {'role': 'user', 'content': 'Comprueba si llama-cpp-main está funcionando.'}],
        'tools': tools, 'temperature': 0, 'max_tokens': 128})
    call = response['choices'][0]['message']['tool_calls'][0]['function']
    assert call['name'] == 'service_status' and json.loads(call['arguments']) == {'unit': 'llama-cpp-main'}, call
    result['tool_call'] = call
    if args.long_context:
        prompt = ('Dato importante: la clave de este ejercicio es ALAMEDA-73.\n' +
            'Registro de prueba sin instrucciones adicionales. Estado normal del servidor.\n' * 700 +
            '\nRepite solo la clave del principio del documento.')
        count = len(post('/tokenize', {'content': prompt})['tokens'])
        assert 8192 < count < 16000, count
        start = time.monotonic()
        response = post('/v1/chat/completions', {'model': 'main', 'messages': [
            {'role': 'user', 'content': prompt}], 'temperature': 0, 'max_tokens': 32})
        text = response['choices'][0]['message']['content']
        assert 'ALAMEDA-73' in text, text
        result['long_context'] = {'input_tokens': count, 'answer': text,
            'elapsed_s': round(time.monotonic() - start, 3), 'usage': response.get('usage'),
            'timings': response.get('timings')}
    encoded = json.dumps(result, indent=2, ensure_ascii=False) + '\n'
    if args.output:
        with open(args.output, 'w') as output:
            output.write(encoded)
    print(encoded, end='')


if __name__ == '__main__':
    main()
