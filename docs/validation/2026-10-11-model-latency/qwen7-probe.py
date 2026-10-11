import json,time,pathlib,urllib.request,subprocess,hashlib,statistics,concurrent.futures
root=pathlib.Path('/srv/ai-lab/llama.cpp/research/qwen7-latency');root.mkdir(parents=True,exist_ok=True)
rev='bb5d59e06d9551d752d08b292a50eb208b07ab1f'
files=[{'path': 'qwen2.5-7b-instruct-q4_k_m-00001-of-00002.gguf', 'size': 3993201344, 'sha256': 'dfce12e3862a5283ccfb88221b48480e58745165de856439950d0f22590580db'}, {'path': 'qwen2.5-7b-instruct-q4_k_m-00002-of-00002.gguf', 'size': 689872288, 'sha256': '539cf93f78e887edea1c04e2d7d8cdaca9d01dae9c9025bcb8accbe29df3d72a'}]
def download(f):
 dest=root/f['path'];tmp=dest.with_suffix('.part')
 if dest.exists() and dest.stat().st_size==f['size'] and hashlib.file_digest(dest.open('rb'),'sha256').hexdigest()==f['sha256']:
  print('REUSING VERIFIED '+f['path'],flush=True);return
 try:
  print('DOWNLOADING '+f['path'],flush=True)
  with urllib.request.urlopen('https://huggingface.co/Qwen/Qwen2.5-7B-Instruct-GGUF/resolve/'+rev+'/'+f['path'],timeout=120) as src,tmp.open('wb') as out:
   while True:
    b=src.read(1048576)
    if not b:break
    out.write(b)
  assert tmp.stat().st_size==f['size']
  assert hashlib.file_digest(tmp.open('rb'),'sha256').hexdigest()==f['sha256']
  tmp.rename(dest);print('VERIFIED '+f['path'],flush=True)
 except BaseException:
  tmp.unlink(missing_ok=True);raise
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:list(pool.map(download,files))
model=root/files[0]['path']
key=next(l.split('=',1)[1].strip().strip(chr(34)).split(';')[0] for l in pathlib.Path('/srv/ai-lab/open-webui/open-webui.env').read_text().splitlines() if l.startswith('OPENAI_API_KEYS='))
print('WAITING for principal inference to finish',flush=True)
for i in range(120):
 req=urllib.request.Request('http://127.0.0.1:8080/metrics',headers={'Authorization':'Bearer '+key})
 try:
  data=urllib.request.urlopen(req,timeout=5).read().decode()
  if 'llamacpp:requests_processing 0' in data and 'llamacpp:requests_deferred 0' in data:break
 except Exception:pass
 time.sleep(3)
else:raise RuntimeError('Production busy; comparison deferred')
cases=[('agenda','La reunión de Atlas es el martes a las 10. ¿Cuándo es? Responde en una frase.'),('summary','Resume en tres puntos: hoy hay que revisar Jira, preparar la reunión con Marta y comprobar el espacio libre del lab. No ejecutes acciones.'),('grounding','Solo sabemos que un servicio devuelve HTTP 200. No hemos medido calidad ni rendimiento. ¿Podemos afirmar que el asistente funciona bien? Responde brevemente.'),('context','Dato: la clave es ALAMEDA-73.\n'+'Registro normal del servidor, sin incidencias.\n'*80+'Repite solamente la clave.')]
results={'blas_threads':8,'runs':[],'files':files,'revision':rev,'context':16384,'threads':8,'cache_prompt':False}
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
   r['rss_kib']=next((int(x.split()[1]) for x in pathlib.Path('/proc/'+str(p.pid)+'/status').read_text().splitlines() if x.startswith('VmRSS:')),0);results['runs'].append(r);print(json.dumps(r,ensure_ascii=False),flush=True)
log=(root/'server.log').open('w')
p=subprocess.Popen(['/opt/llama.cpp/build/bin/llama-server','-m',str(model),'--alias','main','--device','none','-ngl','0','--no-kv-offload','-t','8','-tb','8','-c','16384','-np','1','--host','127.0.0.1','--port','18089','--jinja','--metrics','--cache-ram','0'],stdout=log,stderr=log,env=dict(__import__('os').environ,OPENBLAS_NUM_THREADS='8',OMP_NUM_THREADS='8'))
try:
 for i in range(90):
  if p.poll() is not None:raise RuntimeError('startup failed')
  try:
   urllib.request.urlopen('http://127.0.0.1:18089/health');break
  except Exception:time.sleep(1)
 else:raise RuntimeError('health timeout')
 run('Qwen2.5-7B-Instruct-Q4_K_M',18089,'')
finally:
 p.terminate();p.wait(timeout=15);log.close()
 (root/'results.json').write_text(json.dumps(results,ensure_ascii=False,indent=2)+'\n')
 for f in files:(root/f['path']).unlink(missing_ok=True)
print('DONE',flush=True)
