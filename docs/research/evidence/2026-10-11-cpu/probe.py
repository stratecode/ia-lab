import hashlib,json,pathlib,subprocess,time,tempfile,urllib.request,resource,socket,statistics,os
ROOT=pathlib.Path('/srv/ai-lab/llama.cpp/research/2026-10-11-cpu');ROOT.mkdir(parents=True,exist_ok=True)
REPO='LiquidAI/LFM2.5-1.2B-Instruct-GGUF';REV='8ed288026e23958ad9dfa92d53ed773a8eee7125';NAME='LFM2.5-1.2B-Instruct-Q4_0.gguf'
model=ROOT/NAME
if not model.exists():
 print('Downloading official LFM GGUF',flush=True)
 with urllib.request.urlopen('https://huggingface.co/'+REPO+'/resolve/'+REV+'/'+NAME,timeout=60) as src,model.with_suffix('.part').open('wb') as dst:
  while True:
   chunk=src.read(1024*1024)
   if not chunk:break
   dst.write(chunk)
 model.with_suffix('.part').rename(model)
print('MODEL_BYTES',model.stat().st_size,flush=True)
base=next(pathlib.Path('/srv/ai-lab/llama.cpp/cache/hub/models--Qwen--Qwen2.5-1.5B-Instruct-GGUF/snapshots').glob('*/*.gguf'))
binary='/opt/llama.cpp/build/bin/llama-server'
result={'hardware':'AMD Ryzen 7 7730U; 8 cores/16 threads; AVX2; RAM 30994 MiB','mode':'CPU only: --device none -ngl 0 --no-kv-offload','lfm_revision':REV,'lfm_sha256':hashlib.file_digest(model.open('rb'),'sha256').hexdigest(),'runs':[],'quality':[]}
port=18089
with socket.socket() as s:
 if s.connect_ex(('127.0.0.1',port))==0:raise RuntimeError('Probe port occupied')
def request(path,payload=None):
 req=urllib.request.Request('http://127.0.0.1:'+str(port)+path,data=json.dumps(payload).encode() if payload is not None else None,headers={'Content-Type':'application/json'})
 return json.load(urllib.request.urlopen(req,timeout=60))
def limit():
 resource.setrlimit(resource.RLIMIT_AS,(6*1024**3,6*1024**3))
for label,file in [('Qwen2.5-1.5B-Instruct-Q4_K_M',base),('LFM2.5-1.2B-Instruct-Q4_0',model)]:
 for threads in [4,8]:
  log=ROOT/(label+'-'+str(threads)+'.log')
  with log.open('w') as output:
   proc=subprocess.Popen(['nice','-n','10',binary,'-m',str(file),'--device','none','-ngl','0','--no-kv-offload','-t',str(threads),'-tb',str(threads),'-c','2048','-np','1','--host','127.0.0.1','--port',str(port),'--jinja','--metrics'],stdout=output,stderr=output,preexec_fn=limit)
   try:
    for attempt in range(90):
     if proc.poll() is not None:raise RuntimeError('Model startup failed; see '+str(log))
     try:
      request('/health');break
     except Exception:time.sleep(1)
    else:raise TimeoutError('Model startup')
    print('READY',label,threads,flush=True)
    # Warmup excludes cold load; timed prompts disable prefix cache.
    request('/completion',{'prompt':'Once upon a time','n_predict':16,'temperature':0,'cache_prompt':False})
    for run in range(3):
     prompt=('Describe the practical steps for monitoring a Linux server, checking service status, memory, logs and disk usage. Include concrete examples. '+('The server hosts local language models and a monitoring dashboard. '*14)+'Checklist '+str(run)+':')
     start=time.monotonic();d=request('/completion',{'prompt':prompt,'n_predict':96,'temperature':0,'cache_prompt':False,'ignore_eos':True});elapsed=time.monotonic()-start
     t=d.get('timings',{});rss=next((int(x.split()[1]) for x in pathlib.Path('/proc/'+str(proc.pid)+'/status').read_text().splitlines() if x.startswith('VmRSS:')),0)
     record={'model':label,'threads':threads,'run':run+1,'elapsed_s':round(elapsed,3),'rss_mib':round(rss/1024,1),'timings':t,'generated_tokens':d.get('tokens_predicted'),'prompt_tokens':d.get('tokens_evaluated')}
     result['runs'].append(record);print('MEASUREMENT',json.dumps(record),flush=True)
    if threads==4:
     cases=[('extract','Devuelve SOLO JSON sin markdown con claves service, state, action. Datos: llama-cpp-code está active y no tiene errores. action debe ser none.'),('grounding','Datos observados: los cuatro modelos devuelven HTTP 200. No hay datos de calidad de las respuestas ni de rendimiento. ¿Podemos concluir que el asistente funciona bien? Responde brevemente en español.'),('tool_selection','Devuelve SOLO JSON con claves tool y arguments. Herramientas: service_status(unit) consulta el estado; restart_service(unit) reinicia. El usuario pide: comprueba si llama-cpp-planner está funcionando. No ha solicitado reiniciar. Elige la herramienta apropiada.')]
     for case,prompt in cases:
      d=request('/v1/chat/completions',{'messages':[{'role':'user','content':prompt}],'max_tokens':180,'temperature':0,'cache_prompt':False})
      content=d['choices'][0]['message'].get('content','');result['quality'].append({'model':label,'case':case,'content':content});print('QUALITY',label,case,repr(content),flush=True)
   finally:
    proc.terminate()
    try:proc.wait(timeout=10)
    except subprocess.TimeoutExpired:proc.kill();proc.wait()
  (ROOT/'results.json').write_text(json.dumps(result,indent=2,ensure_ascii=False)+'\n')
print('RESULT_PATH',ROOT/'results.json',flush=True)
