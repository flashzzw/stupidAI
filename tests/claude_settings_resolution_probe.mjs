// Observe the pinned SDK's startup settings resolver. Controlled cases replace
// registry reads; host mode delegates to the real OS and exports no policy body.
import cp from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';
import { promisify } from 'node:util';
import { EventEmitter } from 'node:events';
import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';

const request=JSON.parse(readFileSync(process.argv[2],'utf8')),reads=[],invalidJsonSources=[];
for(const path of [join(process.env.CLAUDE_CONFIG_DIR,'settings.json'),join(request.cwd,'.claude/settings.json'),join(request.cwd,'.claude/settings.local.json')]){
  try{
    const value=JSON.parse(readFileSync(path,'utf8'));
    if(!value||typeof value!=='object'||Array.isArray(value))invalidJsonSources.push(path);
  }catch(error){if(error.code!=='ENOENT')invalidJsonSources.push(path);}
}
const original=cp.execFile;
const absent='ERROR: The system was unable to find the specified registry key or value.';
cp.execFile=(command,...args)=>{
  const callback=args.pop();
  if(typeof callback!=='function')throw Error('unexpected resolver execFile signature');
  const registry=typeof command==='string'&&command.toLowerCase().replaceAll('\\','/').split('/').at(-1)==='reg.exe';
  const call={command:registry?'reg.exe':'other',hive:JSON.stringify(args).includes('HKLM')?'hklm':JSON.stringify(args).includes('HKCU')?'hkcu':'unknown'};
  reads.push(call);
  const done=(error,stdout,stderr)=>{
    const diagnostic=String(stderr??'').trim();
    call.outcome=!error?'present':diagnostic===absent?'absent':'unavailable';
    if(error)call.error_code=String(error.code??'unknown');
    callback(error,stdout,stderr);
  };
  if(request.mode==='host')return Reflect.apply(original,cp,[command,...args,done]);
  if(!registry)throw Error('unexpected subprocess in metadata resolver');
  const denied=request.mode==='denied',unavailable=request.mode==='unavailable';
  const error=Object.assign(new Error('controlled registry read failure'),{code:unavailable?'EACCES':1});
  const stderr=denied?'ERROR: Access is denied.':unavailable?'controlled spawn reg.exe EACCES':absent;
  queueMicrotask(()=>done(error,'',stderr));
  return new EventEmitter();
};
Object.defineProperty(cp.execFile,promisify.custom,{value:(...args)=>new Promise((resolve,reject)=>{
  cp.execFile(...args,(error,stdout,stderr)=>{
    if(error){error.stdout=stdout;error.stderr=stderr;reject(error);}else resolve({stdout,stderr});
  });
})});
syncBuiltinESMExports();
const {resolveSettings}=await import('../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs');
const sdk=JSON.parse(readFileSync(new URL('../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/package.json',import.meta.url),'utf8'));
if(sdk.version!=='0.3.280')throw Error('revalidate SDK pin');
let result;
try{
  const options={cwd:request.cwd,settingSources:['user','project','local'],...(request.mode==='helper'?{serverManagedSettings:{policyHelper:{path:request.helper},enabledPlugins:{'w0208-test@local':true}}}:{})};
  const resolved=await resolveSettings(options);
  result={resolver_returned:true,has_completeness_field:'complete' in resolved,
    has_errors_field:'errors' in resolved,effective_keys:Object.keys(resolved.effective),
    fixture_plugins:Object.fromEntries(Object.entries(resolved.effective.enabledPlugins??{}).filter(([key])=>key.startsWith('w0208-'))),
    policy_helper_reported:!!resolved.effective.policyHelper,
    sources:resolved.sources.map(s=>({source:s.source,path:s.path,keys:Object.keys(s.settings),policy_origin:s.policyOrigin})),
    policy_helper_executed:existsSync(request.helperMarker)};
}catch(error){result={resolver_returned:false,error_name:error?.name??'unknown'};}
result={...result,mode:request.mode,reads,invalid_json_sources:invalidJsonSources,sdk_version:sdk.version,
  provenance_requires_reconciliation:reads.some(r=>r.outcome==='unavailable')||invalidJsonSources.length>0||result.policy_helper_reported===true||!result.resolver_returned,
  scope:request.mode==='host'?'real SDK and read-only registry attempts; no managed policy contents exported':'real SDK, controlled registry replies; policyHelper remote settings injected only in helper case'};
writeFileSync(join(request.root,'result.json'),JSON.stringify(result,null,2));
console.log(JSON.stringify(result));
