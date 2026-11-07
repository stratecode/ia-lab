import json, subprocess, time, urllib.request, uuid, re, os, sys, glob
URL = os.environ.get("WHISPER_URL", "http://127.0.0.1:8193") + "/inference"
PHRASES = {
 "corta": "¿Qué tengo pendiente en Jira?",
 "tecnica": "Dime cómo va la tarea PMI sesenta y siete y qué dijo Gustavo en Granola.",
 "media": "Tienes veinticinco incidencias abiertas: tres en curso, tres bloqueadas y diecinueve por hacer. Yo empezaría por la más antigua, que lleva vencida desde julio.",
 "larga": "El sprint de tres semanas termina el viernes. Valentina tiene en curso la autenticación y el alta de clientes, y Gonzalo está con las traducciones personalizadas. Hay una tarea bloqueada que depende de la verificación bancaria, y de momento nadie la ha desbloqueado.",
}
AUDIO = "/srv/ai-lab/donna-eval/audio"; os.makedirs(AUDIO, exist_ok=True)
def norm(s): return re.sub(r"[^a-záéíóúñ0-9 ]", "", s.lower()).split()
def wav16(label, text):
    path = f"{AUDIO}/{label}.wav"
    if not os.path.exists(path):
        req = urllib.request.Request("http://127.0.0.1:8084/synthesize", data=json.dumps({"text": text}).encode(), headers={"Content-Type": "application/json"})
        raw = urllib.request.urlopen(req, timeout=120).read()
        out = subprocess.run(["ffmpeg","-v","error","-i","pipe:0","-ac","1","-ar","16000","-f","wav","pipe:1"], input=raw, capture_output=True).stdout
        open(path, "wb").write(out)
    return open(path, "rb").read()
def stt(payload, prompt):
    b = uuid.uuid4().hex; fields = {"response_format":"json","temperature":"0","language":"es"}
    if prompt: fields["prompt"] = prompt
    body = b""
    for k, v in fields.items(): body += f'--{b}\r\nContent-Disposition: form-data; name="{k}"\r\n\r\n{v}\r\n'.encode()
    body += f'--{b}\r\nContent-Disposition: form-data; name="file"; filename="v.wav"\r\nContent-Type: audio/wav\r\n\r\n'.encode() + payload + f"\r\n--{b}--\r\n".encode()
    req = urllib.request.Request(URL, data=body, headers={"Content-Type": f"multipart/form-data; boundary={b}"})
    t = time.monotonic(); res = urllib.request.urlopen(req, timeout=300).read()
    return json.loads(res)["text"].strip(), time.monotonic() - t
prompt = sys.argv[1] if len(sys.argv) > 1 else ""
for label, text in PHRASES.items():
    payload = wav16(label, text); stt(payload, prompt)  # warm-up
    times = []; heard = ""
    for _ in range(2):
        heard, s = stt(payload, prompt); times.append(s)
    a, b = norm(text), norm(heard)
    print(json.dumps({"frase": label, "whisper_s": round(sum(times)/len(times), 2), "palabras": round(sum(1 for w in a if w in b)/len(a), 2), "oido": heard[:70]}, ensure_ascii=False), flush=True)
