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
tools=[{'type':'function','function':{'name':'service_status','description':'Consulta el estado de una unidad del lab sin modificarla','parameters':{'type':'object','properties':{'unit':{'type':'string','enum':['llama-cpp-code','llama-cpp-planner','llama-cpp-utility']}},'required':['unit'],'additionalProperties':False}}}]
result['native_tools']=[]
for label,file in [('Qwen2.5-1.5B-Instruct-Q4_K_M',base),('LFM2.5-1.2B-Instruct-Q4_0',model)]:
 with (ROOT/(label+'-tools.log')).open('w') as output:
  proc=subprocess.Popen([binary,'-m',str(file),'--device','none','-ngl','0','--no-kv-offload','-t','4','-tb','4','-c','2048','-np','1','--host','127.0.0.1','--port',str(port),'--jinja'],stdout=output,stderr=output,preexec_fn=limit)
  try:
   for attempt in range(90):
    if proc.poll() is not None:raise RuntimeError('startup failed')
    try:request('/health');break
    except Exception:time.sleep(1)
   for unit in ['llama-cpp-planner','llama-cpp-code']:
    d=request('/v1/chat/completions',{'messages':[{'role':'system','content':'Eres un asistente operativo. Usa la herramienta disponible para consultar el estado solicitado. No inventes resultados.'},{'role':'user','content':'Comprueba si '+unit+' está funcionando.'}],'tools':tools,'tool_choice':'auto','max_tokens':180,'temperature':0})
    r={'model':label,'expected_unit':unit,'message':d['choices'][0]['message']};result['native_tools'].append(r);print(json.dumps(r,ensure_ascii=False),flush=True)
  finally:
   proc.terminate()
   try:proc.wait(timeout=10)
   except subprocess.TimeoutExpired:proc.kill();proc.wait()
(ROOT/'native-tools.json').write_text(json.dumps(result['native_tools'],indent=2,ensure_ascii=False)+'\n')
