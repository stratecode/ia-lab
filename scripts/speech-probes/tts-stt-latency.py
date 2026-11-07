import json, subprocess, time, wave, io, urllib.request, uuid, os, sys, re
PHRASES = {
 "corta (~2 s)": "¿Qué tengo pendiente en Jira?",
 "media (~8 s)": "Tienes veinticinco incidencias abiertas: tres en curso, tres bloqueadas y diecinueve por hacer. Yo empezaría por la más antigua, que lleva vencida desde julio.",
 "larga (~20 s)": "El sprint de tres semanas termina el viernes. Valentina tiene en curso la autenticación y el alta de clientes, y Gonzalo está con las traducciones personalizadas. Hay una tarea bloqueada que depende de la verificación bancaria, y de momento nadie la ha desbloqueado. Si quieres, te detallo las bloqueadas una por una.",
}
def post(url, data, headers):
    req = urllib.request.Request(url, data=data, headers=headers)
    t = time.monotonic()
    with urllib.request.urlopen(req, timeout=300) as r: body = r.read()
    return body, time.monotonic() - t
def dur(wav):
    w = wave.open(io.BytesIO(wav)); return w.getnframes() / w.getframerate(), w.getframerate()
def multipart(fields, fname, payload):
    b = uuid.uuid4().hex; out = b""
    for k, v in fields.items(): out += f'--{b}\r\nContent-Disposition: form-data; name="{k}"\r\n\r\n{v}\r\n'.encode()
    out += f'--{b}\r\nContent-Disposition: form-data; name="file"; filename="{fname}"\r\nContent-Type: audio/wav\r\n\r\n'.encode() + payload + f"\r\n--{b}--\r\n".encode()
    return out, f"multipart/form-data; boundary={b}"
def norm(s): return re.sub(r"[^a-záéíóúñ0-9 ]", "", s.lower()).split()
for label, text in PHRASES.items():
    for rep in range(2):
        wav, tts_s = post("http://127.0.0.1:8084/synthesize", json.dumps({"text": text}).encode(), {"Content-Type": "application/json"})
        seconds, rate = dur(wav)
        p = subprocess.run(["ffmpeg","-v","error","-i","pipe:0","-ac","1","-ar","16000","-f","wav","pipe:1"], input=wav, capture_output=True)
        wav16 = p.stdout
        body, ctype = multipart({"response_format":"json","temperature":"0","language":"es"}, "voice.wav", wav16)
        res, stt_s = post("http://127.0.0.1:8083/inference", body, {"Content-Type": ctype})
        heard = json.loads(res)["text"].strip()
        a, b = norm(text), norm(heard)
        hit = sum(1 for w in a if w in b) / len(a)
        print(json.dumps({"frase": label, "audio_s": round(seconds,1), "piper_s": round(tts_s,2), "piper_rtf": round(tts_s/seconds,2),
                          "whisper_s": round(stt_s,2), "whisper_rtf": round(stt_s/seconds,2), "palabras_recuperadas": round(hit,2), "rep": rep}), flush=True)
    print("   oído:", heard[:120])
