import json,time,pathlib,urllib.request,subprocess,hashlib,statistics
root=pathlib.Path('/srv/ai-lab/llama.cpp/research/latency');root.mkdir(parents=True,exist_ok=True)
model=root/'LFM2.5-1.2B-Instruct-Q4_0.gguf'
if not model.exists():
 print('download',flush=True)
 with urllib.request.urlopen('https://huggingface.co/LiquidAI/LFM2.5-1.2B-Instruct-GGUF/resolve/8ed288026e23958ad9dfa92d53ed773a8eee7125/'+model.name,timeout=120) as src,model.open('wb') as dst:
  while True:
   b=src.read(1048576)
   if not b:break
   dst.write(b)
key=''
for line in pathlib.Path('/srv/ai-lab/open-webui/open-webui.env').read_text().splitlines():
 if line.startswith('OPENAI_API_KEYS='):key=line.split('=',1)[1].strip().strip('"').split(';')[0]
cases=[('agenda','La reunión de Atlas es el martes a las 10. ¿Cuándo es? Responde en una frase.'),('summary','Resume en tres puntos: hoy hay que revisar Jira, preparar la reunión con Marta y comprobar el espacio libre del lab. No ejecutes acciones.'),('grounding','Solo sabemos que un servicio devuelve HTTP 200. No hemos medido calidad ni rendimiento. ¿Podemos afirmar que el asistente funciona bien? Responde brevemente.'),('context','Dato: la clave es ALAMEDA-73.\n'+'Registro normal del servidor, sin incidencias.\n'*80+'Repite solamente la clave.')]
results={'runs':[],'sha256':hashlib.file_digest(model.open('rb'),'sha256').hexdigest(),'context':16384,'threads':8,'cache_prompt':False}
def run(label,port,auth):
 headers={'Content-Type':'application/json'}
 if auth:headers['Authorization']='Bearer '+auth
 for case,prompt in cases:
  for rep in range(3):
   body={'model':'main','messages':[{'role':'user','content':prompt}],'max_tokens':128,'temperature':0,'cache_prompt':False,'stream':True,'stream_options':{'include_usage':True}}
   start=time.monotonic();first=None;chunks=[];timings={};usage={}
   with urllib.request.urlopen(urllib.request.Request('http://127.0.0.1:'+str(port)+'/v1/chat/completions',data=json.dumps(body).encode(),headers=headers),timeout=180) as response:
    for line in response:
     if not line.startswith(b'data: ') or line.strip()==b'data: [DONE]':continue
     d=json.loads(line[6:]);timings=d.get('timings') or timings;usage=d.get('usage') or usage
     for c in d.get('choices',[]):
      text=c.get('delta',{}).get('content') or ''
      if text and first is None:first=time.monotonic()-start
      chunks.append(text)
   r={'model':label,'case':case,'rep':rep+1,'ttft_s':round(first,3) if first else None,'total_s':round(time.monotonic()-start,3),'answer':''.join(chunks),'timings':timings,'usage':usage}
   results['runs'].append(r);print(json.dumps(r,ensure_ascii=False),flush=True)
run('Qwen3.5-4B-Q4_K_M',8080,key)
log=(root/'server.log').open('w')
p=subprocess.Popen(['/opt/llama.cpp/build/bin/llama-server','-m',str(model),'--alias','main','--device','none','-ngl','0','--no-kv-offload','-t','8','-tb','8','-c','16384','-np','1','--host','127.0.0.1','--port','18089','--jinja','--metrics','--cache-ram','0'],stdout=log,stderr=log)
try:
 for i in range(90):
  if p.poll() is not None:raise RuntimeError('startup failed')
  try:
   urllib.request.urlopen('http://127.0.0.1:18089/health');break
  except Exception:time.sleep(1)
 else:raise RuntimeError('health timeout')
 run('LFM2.5-1.2B-Q4_0',18089,'')
finally:
 p.terminate();p.wait(timeout=15);log.close()
 (root/'results.json').write_text(json.dumps(results,ensure_ascii=False,indent=2)+'\n')
 model.unlink()
print('DONE',flush=True)
