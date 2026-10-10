// Run in a fresh metadata-only Node process. The SDK suppresses some source
// read errors, so observe those reads without patching a live Session process.
import fs, { type BigIntStats } from 'node:fs';
import cp from 'node:child_process';
import { createHash } from 'node:crypto';
import { syncBuiltinESMExports } from 'node:module';
import { promisify } from 'node:util';
import { isAbsolute } from 'node:path';
import type { ResolvedSettings } from '@anthropic-ai/claude-agent-sdk';

type ReadIssue = { path: string; operation: string; code: string };
type RegistryRead = { hive: string; outcome: 'present' | 'absent' | 'unavailable' | 'invalid_json' | 'invalid_value'; code?: string };
type FileRead = { path: string; status: string; sha256?: string; bytes?: number; resolved_path?: string; identity_sha256?: string };
type FileBinding = { resolved_path: string; identity_sha256: string; size: bigint };
type Request = { cwd: string; additional_files: string[] };
type StartupConflict = { source: string; path?: string; policy_origin?: string; kind: 'api_key_helper' | 'route_environment' | 'state_environment' | 'invalid_environment' };
const hash=(body:Buffer)=>createHash('sha256').update(body).digest('hex');
// atime can change merely by reading; the other fields identify the object,
// its access metadata and the version whose bytes were actually observed.
const statKey=(s:BigIntStats)=>[s.dev,s.ino,s.mode,s.uid,s.gid,s.nlink,s.size,s.mtimeNs,s.ctimeNs].map(String).join(':');
const errorCode=(error:unknown)=>typeof error==='object'&&error!==null&&'code' in error?String(error.code):'unavailable';
// The fixed CLI accepts this platform map although SDK 0.3.280 only types
// policyHelper. The resolver preserves it; evaluating it belongs to startup.
const hasPolicyHelpers=(settings:object)=>'policyHelpers' in settings&&settings.policyHelpers!=null;
const jsonObject=(body:Buffer):Record<string,unknown>=>{
  const value:unknown=JSON.parse(body.toString('utf8').replace(/^\uFEFF/,''));
  if(!value||typeof value!=='object'||Array.isArray(value))throw Error('invalid object');
  return value as Record<string,unknown>;
};
const registryValue=(stdout:string):{outcome:RegistryRead['outcome'];settings?:Record<string,unknown>}=>{
  // Match the fixed SDK's REG_SZ / REG_EXPAND_SZ extraction. A successful
  // command can still contain no Settings value or an unreadable document.
  const body=stdout.match(/^[ \t]+Settings[ \t]+REG_(?:EXPAND_)?SZ[ \t]+([\s\S]*)/im)?.[1]?.trimEnd();
  if(!body)return {outcome:'invalid_value'};
  try{return {outcome:'present',settings:jsonObject(Buffer.from(body))};}catch{return {outcome:'invalid_json'};}
};

export async function inspectStartupSettings(request:Request) {
  const sdkVersion=jsonObject(fs.readFileSync(new URL('./package.json',import.meta.resolve('@anthropic-ai/claude-agent-sdk')))).version;
  if(sdkVersion!=='0.3.280')throw Error('unsupported settings resolver version');
  if(!isAbsolute(request.cwd)||request.cwd.includes('\0')||!Array.isArray(request.additional_files)||request.additional_files.length>8||request.additional_files.some(p=>typeof p!=='string'||!isAbsolute(p)||p.includes('\0')))throw Error('invalid source request');
  const files=new Map<string,FileRead>(),issues:ReadIssue[]=[],registry:RegistryRead[]=[];
  const conflicts=new Map<string,StartupConflict>();
  const observeConflicts=(settings:object,source:string,path?:string,policyOrigin?:string)=>{
    if(path?.includes('/node_modules/'))return;
    const add=(kind:StartupConflict['kind'])=>{
      const conflict={source,...(path?{path}:{}),...(policyOrigin?{policy_origin:policyOrigin}:{}),kind};
      conflicts.set(JSON.stringify(conflict),conflict);
    };
    // Apply the same binding boundary as the registered-file Go checks. Do
    // not export variable names, helper commands or credential values.
    if('apiKeyHelper' in settings)add('api_key_helper');
    if('env' in settings){
      const env=settings.env;
      if(!env||typeof env!=='object'||Array.isArray(env)){add('invalid_environment');return;}
      for(const [key,value] of Object.entries(env)){
        if(typeof value!=='string')add('invalid_environment');
        if(key==='CLAUDE_CONFIG_DIR'||key==='CLAUDE_CODE_PROJECT_DIR_NAME'||key==='HOME'||key.startsWith('XDG_'))add('state_environment');
        if(key.startsWith('ANTHROPIC_')||key.startsWith('CLAUDE_CODE_USE_'))add('route_environment');
      }
    }
  };
  const opened=new Map<number,{path:string;chunks:Buffer[];bytes:number;binding?:FileBinding}>();
  const bindings=new Map<string,FileBinding>();
  const resolutions=new Map<string,FileBinding>();
  const originals={open:fs.openSync,read:fs.readSync,close:fs.closeSync,stat:fs.statSync,lstat:fs.lstatSync,readdir:fs.readdirSync,fstat:fs.fstatSync,realpath:fs.realpathSync,exec:cp.execFile};
  let totalBytes=0,overflow=false;
  const sourcePath=(path:unknown)=>typeof path==='string'&&isAbsolute(path)&&path.endsWith('.json');
  const recordFile=(next:FileRead)=>{
    const previous=files.get(next.path);
    if(previous?.status==='unavailable')next.status='unavailable';
    else if(previous&&(previous.status==='changed_during_read'||previous.sha256&&next.sha256&&previous.sha256!==next.sha256||previous.identity_sha256&&next.identity_sha256&&previous.identity_sha256!==next.identity_sha256||((previous.status==='absent')!==(next.status==='absent'))&&(previous.sha256||next.sha256)))next.status='changed_during_read';
    files.set(next.path,{...previous,...next});
  };
  const issue=(path:string,operation:string,error:unknown)=>{
    const code=errorCode(error);
    if(code!=='ENOENT'&&issues.length<128)issues.push({path,operation,code});
    if(sourcePath(path))recordFile({path,status:code==='ENOENT'?'absent':'unavailable'});
  };
  const bindingAt=(path:string,fd?:number):FileBinding=>{
    const target=originals.stat(path,{bigint:true}),entry=originals.lstat(path,{bigint:true});
    const resolved=originals.realpath(path),file=fd===undefined?target:originals.fstat(fd,{bigint:true});
    if(statKey(file)!==statKey(target))throw Object.assign(Error('source binding changed'),{code:'ESTALE'});
    return {resolved_path:resolved,identity_sha256:hash(Buffer.from(JSON.stringify([resolved,statKey(file),statKey(entry)]))),size:file.size};
  };
  const bindingError=(path:string,error:unknown)=>{
    if(['ENOENT','ENOTDIR','ESTALE'].includes(errorCode(error)))recordFile({path,status:'changed_during_read'});
    else issue(path,'identity',error);
  };
  const verifyBinding=(path:string,before:FileBinding,fd?:number)=>{
    try{if(bindingAt(path,fd).identity_sha256!==before.identity_sha256)recordFile({path,status:'changed_during_read'});}
    catch(error){bindingError(path,error);}
  };
  const beginRead=(fd:number,path:string)=>{
    const stat=originals.fstat(fd,{bigint:true});
    if(!stat.isFile()||stat.size>1n<<20n)throw Object.assign(Error('source exceeds read boundary'),{code:'EFBIG'});
    let binding:FileBinding|undefined;
    try{binding=bindingAt(path,fd);bindings.set(path,binding);}catch(error){bindingError(path,error);}
    opened.set(fd,{path,chunks:[],bytes:0,binding});
  };
  const captureRead=(fd:number,buffer:unknown,offset:number,n:number)=>{
    const entry=opened.get(fd);if(!entry||n<=0)return;
    if(!ArrayBuffer.isView(buffer)||!Number.isSafeInteger(offset)||offset<0){overflow=true;return;}
    entry.bytes+=n;totalBytes+=n;
    if(entry.bytes<=1<<20&&totalBytes<=8<<20)entry.chunks.push(Buffer.from(Buffer.from(buffer.buffer,buffer.byteOffset+offset,n)));else overflow=true;
  };
  const finish=(fd:number)=>{
    const entry=opened.get(fd);if(!entry)return;opened.delete(fd);
    if(entry.bytes>1<<20||overflow){recordFile({path:entry.path,status:'limit_exceeded'});return;}
    const body=Buffer.concat(entry.chunks);
    let status='read';try{observeConflicts(jsonObject(body),'file',entry.path);}catch{status='invalid_json';}
    if(entry.binding&&BigInt(entry.bytes)!==entry.binding.size)status='changed_during_read';
    recordFile({path:entry.path,status,sha256:hash(body),bytes:body.length,...(entry.binding?{resolved_path:entry.binding.resolved_path,identity_sha256:entry.binding.identity_sha256}:{})});
    if(entry.binding)verifyBinding(entry.path,entry.binding,fd);
  };
  fs.openSync=((...args:Parameters<typeof fs.openSync>)=>{
    try{const fd=Reflect.apply(originals.open,fs,args) as number;if(sourcePath(args[0])){
      try{beginRead(fd,String(args[0]));}catch(error){originals.close(fd);throw error;}
    }return fd;}
    catch(error){if(sourcePath(args[0]))issue(String(args[0]),'open',error);throw error;}
  }) as typeof fs.openSync;
  fs.readSync=((...args:unknown[])=>{
    try{
      const n=Reflect.apply(originals.read,fs,args) as number,options=args[2];
      const offset=typeof options==='number'?options:typeof options==='object'&&options!==null&&'offset' in options?Number(options.offset):0;
      captureRead(Number(args[0]),args[1],offset,n);return n;
    }catch(error){const entry=opened.get(Number(args[0]));if(entry)issue(entry.path,'read',error);throw error;}
  }) as typeof fs.readSync;
  fs.closeSync=(fd:number)=>{try{finish(fd);}finally{originals.close(fd);}};
  const observeRealpath=(original:typeof originals.realpath|typeof originals.realpath.native)=>(...args:unknown[])=>{
    let resolved:string|Buffer;
    try{resolved=Reflect.apply(original,fs,args) as string|Buffer;}
    catch(error){if(sourcePath(args[0]))issue(String(args[0]),'realpath',error);throw error;}
    if(sourcePath(args[0])){
      const path=String(args[0]);
      try{
        const binding=bindingAt(path),previous=resolutions.get(path);
        if(binding.resolved_path!==String(resolved)||previous&&previous.identity_sha256!==binding.identity_sha256)recordFile({path,status:'changed_during_read'});
        resolutions.set(path,binding);
      }catch(error){bindingError(path,error);}
    }
    return resolved;
  };
  Object.assign(fs,{realpathSync:Object.assign(observeRealpath(originals.realpath),{native:observeRealpath(originals.realpath.native)})});
  for(const [name,original] of [['statSync',originals.stat],['lstatSync',originals.lstat],['readdirSync',originals.readdir]] as const){
    // Preserve overloads and arguments; inspect only the failure metadata.
    const wrapped=(...args:unknown[])=>{try{return Reflect.apply(original,fs,args);}catch(error){if(typeof args[0]==='string')issue(args[0],name,error);throw error;}};
    Object.assign(fs,{[name]:wrapped});
  }
  cp.execFile=((command:string,...args:unknown[])=>{
    const callback=args.pop();if(typeof callback!=='function')throw Error('unsupported subprocess signature');
    const executable=command.toLowerCase().replaceAll('\\','/').split('/').at(-1);
    if(executable!=='reg.exe')throw Error('unexpected metadata subprocess');
    const encoded=JSON.stringify(args),row:RegistryRead={hive:encoded.includes('HKLM')?'hklm':encoded.includes('HKCU')?'hkcu':'unknown',outcome:'unavailable'};registry.push(row);
    return Reflect.apply(originals.exec,cp,[command,...args,(error:unknown,stdout:string,stderr:string)=>{
      const value=!error?registryValue(stdout):undefined;
      row.outcome=value?.outcome??(String(stderr).trim()==='ERROR: The system was unable to find the specified registry key or value.'?'absent':'unavailable');
      if(value?.settings)observeConflicts(value.settings,'registry',undefined,row.hive);
      if(error)row.code=errorCode(error);callback(error,stdout,stderr);
    }]);
  }) as typeof cp.execFile;
  Object.defineProperty(cp.execFile,promisify.custom,{value:(...args:unknown[])=>new Promise((resolve,reject)=>{
    Reflect.apply(cp.execFile,cp,[...args,(error:unknown,stdout:string,stderr:string)=>{
      if(error&&typeof error==='object'){Object.assign(error,{stdout,stderr});reject(error);}else resolve({stdout,stderr});
    }]);
  })});
  syncBuiltinESMExports();
  let resolved:ResolvedSettings|undefined,error=false;
  try{
    const sdk=await import('@anthropic-ai/claude-agent-sdk');
    resolved=await sdk.resolveSettings({cwd:request.cwd,settingSources:['user','project','local']});
  }catch{error=true;}
  finally{
    for(const fd of opened.keys())finish(fd);
    Object.assign(fs,{openSync:originals.open,readSync:originals.read,closeSync:originals.close,statSync:originals.stat,lstatSync:originals.lstat,readdirSync:originals.readdir,realpathSync:originals.realpath});cp.execFile=originals.exec;
    syncBuiltinESMExports();
  }
  // Git/cwd and custom flag files come from the caller's saved source graph;
  // they are read separately, never misrepresented as SDK-discovered sources.
  for(const path of request.additional_files){
    try{
      const fd=originals.open(path,fs.constants.O_RDONLY|fs.constants.O_NONBLOCK|fs.constants.O_NOFOLLOW);
      try{
        beginRead(fd,path);
        const buffer=Buffer.alloc(65536);let n=0;
        while((n=originals.read(fd,buffer,0,buffer.length,null))>0){
          captureRead(fd,buffer,0,n);
          if(overflow)throw Object.assign(Error('source read limit'),{code:'EFBIG'});
        }
      }
      finally{try{finish(fd);}finally{originals.close(fd);}}
    }catch(e){issue(path,'additional_file',e);}
  }
  // Also cover replacement after close or while another source is being read.
  // This is a bounded observation, not a lock against later external changes.
  for(const [path,binding] of bindings)verifyBinding(path,binding);
  for(const [path,binding] of resolutions)verifyBinding(path,binding);
  for(const file of files.values()){
    if(file.status!=='absent')continue;
    try{originals.lstat(file.path);recordFile({path:file.path,status:'changed_during_read'});}
    catch(error){if(errorCode(error)!=='ENOENT')issue(file.path,'identity',error);}
  }
  for(const layer of resolved?.sources??[])observeConflicts(layer.settings,layer.source,layer.path,layer.policyOrigin);
  if(resolved)observeConflicts(resolved.effective,'effective');
  const sources=(resolved?.sources??[]).map(s=>({source:s.source,...(s.path?{path:s.path}:{}),...(s.policyOrigin?{policy_origin:s.policyOrigin}:{}),settings_sha256:hash(Buffer.from(JSON.stringify(s.settings))),has_policy_helper:!!s.settings.policyHelper,has_policy_helpers:hasPolicyHelpers(s.settings)}));
  for(const source of sources){if(source.path&&!files.get(source.path)?.sha256&&issues.length<128)issues.push({path:source.path,operation:'coverage',code:'source_read_unobserved'});}
  const helper=!!resolved?.effective.policyHelper;
  const helpers=!!resolved&&hasPolicyHelpers(resolved.effective);
  const relevant=[...files.values()].filter(f=>!f.path.includes('/node_modules/')).sort((a,b)=>a.path.localeCompare(b.path));
  const pathBindings=[...resolutions].filter(([path])=>!path.includes('/node_modules/')).map(([path,b])=>({path,resolved_path:b.resolved_path,identity_sha256:b.identity_sha256})).sort((a,b)=>a.path.localeCompare(b.path));
  const readComplete=!error&&!overflow&&!issues.length&&registry.every(r=>r.outcome==='present'||r.outcome==='absent')&&relevant.every(f=>['read','absent'].includes(f.status));
  const startupConflicts=[...conflicts].sort(([a],[b])=>a.localeCompare(b)).map(([,value])=>value);
  return {version:1,sdk_version:sdkVersion,source:'claude.sdk.observed_startup_reads',cwd:request.cwd,at:new Date().toISOString(),resolver_returned:!error,read_complete:readComplete,complete:false,files:relevant,path_bindings:pathBindings,sources,registry,issues,policy_helper_present:helper,policy_helpers_present:helpers,startup_conflicts:startupConflicts,
    limitations:['read_observation_not_admission','sdk_local_graph_differs_from_native','remote_policy_not_fetched',...(helper?['policy_helper_not_executed']:[]),...(helpers?['policy_helpers_not_evaluated']:[])]};
}

if(process.argv[2]==='--stdio'){
  let input='';for await(const chunk of process.stdin){input+=chunk;if(input.length>65536)throw Error('request too large');}
  try{process.stdout.write(JSON.stringify(await inspectStartupSettings(JSON.parse(input)))+'\n');}
  catch{process.stdout.write(JSON.stringify({error:'startup source inspection unavailable'})+'\n');process.exitCode=1;}
}
