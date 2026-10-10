// Real pinned SDK with controlled OS failures; every case has a fresh process.
import fs from 'node:fs';
import cp from 'node:child_process';
import {EventEmitter} from 'node:events';
import {syncBuiltinESMExports} from 'node:module';
import {promisify} from 'node:util';
import {createHash} from 'node:crypto';
import {join} from 'node:path';
const input=JSON.parse(fs.readFileSync(process.argv[2],'utf8')),calls=[],registryReplies=[];
const original={open:fs.openSync,read:fs.readSync,close:fs.closeSync,stat:fs.statSync,lstat:fs.lstatSync,readdir:fs.readdirSync,fstat:fs.fstatSync,realpath:fs.realpathSync};
const policyPath=join(input.root,'controlled-policy/managed-settings.json'),policyLogical='/etc/claude-code/managed-settings.json';
const managed=input.mode.startsWith('managed-'),managedIOError=input.mode==='managed-realpath-error',mutatingManaged=managed&&!managedIOError&&!['managed-stable','managed-stable-link'].includes(input.mode);
const policyBytes=managed?fs.readFileSync(policyPath):null;
const identity=path=>{
 try{
  const s=original.stat(path,{bigint:true}),l=original.lstat(path,{bigint:true});
  return {resolved_path:original.realpath(path),dev:String(s.dev),ino:String(s.ino),mode:String(s.mode),mtime_ns:String(s.mtimeNs),ctime_ns:String(s.ctimeNs),path_ino:String(l.ino),path_mode:String(l.mode)};
 }catch(error){return {error:error.code};}
};
const fileChange={mode:input.mode,before:managed?identity(policyPath):null,mutations:[]};
const policyAliases=new Set([policyLogical,policyPath,...(managed?[fileChange.before.resolved_path]:[])]);
let mutated=false,policyResolutionSeen=false,realpathFailureInjected=false;
const mutatePolicy=()=>{
 if(!mutatingManaged||mutated)return;
 mutated=true;
 if(['managed-replace','managed-replace-on-close','managed-before-open'].includes(input.mode)){
  const replacement=policyPath+'.replacement';
  const fd=original.open(replacement,'wx',Number(original.stat(policyPath,{bigint:true}).mode&0o777n));
  try{fs.writeSync(fd,policyBytes);}finally{original.close(fd);}
  fs.renameSync(replacement,policyPath);
 }else if(input.mode==='managed-chmod'){
  fs.chmodSync(policyPath,Number(original.stat(policyPath,{bigint:true}).mode&0o777n)^0o040);
 }else if(input.mode==='managed-touch'){
  const s=original.stat(policyPath);fs.utimesSync(policyPath,s.atime,new Date(s.mtimeMs+2000));
 }else if(input.mode==='managed-retarget'){
  fs.unlinkSync(policyPath);fs.symlinkSync(join(input.root,'controlled-policy/targets/second.json'),policyPath);
 }else if(input.mode==='managed-disappears'){
  fs.unlinkSync(policyPath);
 }
 fileChange.mutations.push({at:'after_owned_io',after:identity(policyPath)});
};
const fdPaths=new Map();
const mapPath=p=>{
 if(typeof p!=='string')return p;
 for(const [prefix,folder] of [['/etc/claude-code/','controlled-policy'],['/mnt/c/Program Files/ClaudeCode/','controlled-windows-policy']]){
  if(p.startsWith(prefix))return join(input.root,folder,p.slice(prefix.length));
 }
 return p;
};
fs.openSync=(path,...rest)=>{
 if(input.mode==='managed-before-open'&&policyResolutionSeen&&path===policyLogical)mutatePolicy();
 const fd=original.open(mapPath(path),...rest);fdPaths.set(fd,String(path));return fd;
};
fs.readSync=(fd,...rest)=>{
 if(input.mode==='read-error'&&fdPaths.get(fd)===input.project)throw Object.assign(new Error('controlled read failure'),{code:'EIO'});
 const n=original.read(fd,...rest);
 if(n>0&&policyAliases.has(fdPaths.get(fd))&&input.mode!=='managed-replace-on-close')mutatePolicy();
 return n;
};
fs.closeSync=fd=>{
 const path=fdPaths.get(fd);fdPaths.delete(fd);const result=original.close(fd);
 if(input.mode==='source-disappears'&&path===input.project&&fs.existsSync(path))fs.unlinkSync(path);
 if(policyAliases.has(path)&&input.mode==='managed-replace-on-close')mutatePolicy();
 return result;
};
for(const [name,fn] of [['statSync',original.stat],['lstatSync',original.lstat],['readdirSync',original.readdir]])fs[name]=(path,...rest)=>fn(mapPath(path),...rest);
const mappedRealpath=(fn,path,...rest)=>{
 if(managedIOError&&path===policyLogical&&!realpathFailureInjected){
  realpathFailureInjected=true;throw Object.assign(new Error('controlled one-time resolution failure'),{code:'EIO'});
 }
 const resolved=fn(mapPath(path),...rest);
 // Keep this fixture's virtual policy path canonical, as on a normal /etc
 // file, to exercise resolution and open using the very same path key.
 if(input.mode==='managed-before-open'&&path===policyLogical){policyResolutionSeen=true;return policyLogical;}
 return resolved;
};
fs.realpathSync=Object.assign((...args)=>mappedRealpath(original.realpath,...args),{native:(...args)=>mappedRealpath(original.realpath.native,...args)});
cp.execFile=(command,...args)=>{
 const callback=args.pop();calls.push(String(command));
 if(!String(command).toLowerCase().endsWith('reg.exe'))throw Error('unexpected process');
 const argv=args[0],hive=argv[1].startsWith('HKLM')?'hklm':'hkcu';
 if(argv[0]!=='query'||argv[2]!=='/v'||argv[3]!=='Settings')throw Error('unexpected registry query');
 let error=null,stdout='',stderr='';
 if(input.mode.startsWith('registry-')&&input.mode!=='registry-denied'){
  const settings={wslInheritsWindowsSettings:true,enabledPlugins:{'owned@registry':false},env:{OWNED_REGISTRY_SOURCE:'PRIVATE_PREFLIGHT_SENTINEL'}};
  if(input.mode==='registry-binding-route'||input.mode==='registry-binding-hkcu'&&hive==='hkcu')settings.env.ANTHROPIC_BASE_URL='PRIVATE_PREFLIGHT_SENTINEL';
  if(input.mode==='registry-binding-invalid-env')settings.env.OWNED_INVALID=42;
  let body=JSON.stringify(settings),name='Settings',type='REG_SZ';
  if(input.mode==='registry-expand'){body=JSON.stringify(settings,null,2);name='sEtTiNgS';type='reg_expand_sz';}
  if(input.mode==='registry-empty-object')body='{}';
  if(input.mode==='registry-hkcu-valid'&&hive==='hklm')body=JSON.stringify({wslInheritsWindowsSettings:true});
  if(input.mode==='registry-malformed'||input.mode==='registry-hkcu-malformed'&&hive==='hkcu')body='{broken PRIVATE_PREFLIGHT_SENTINEL';
  if(input.mode==='registry-empty-value')body='';
  if(input.mode==='registry-missing-value')name='Unrelated';
  if(input.mode==='registry-wrong-type'){type='REG_DWORD';body='0x0';}
  if(input.mode==='registry-array')body='[]';
  if(input.mode==='registry-null')body='null';
  stdout='\r\n'+(hive==='hklm'?'HKEY_LOCAL_MACHINE':'HKEY_CURRENT_USER')+'\\SOFTWARE\\Policies\\ClaudeCode\r\n    '+name+'    '+type+'    '+body+'\r\n';
  if(input.mode==='registry-empty-success')stdout='';
 }else{
  error=Object.assign(new Error('controlled registry result'),{code:1});
  stderr=input.mode==='registry-denied'?'ERROR: Access is denied.':'ERROR: The system was unable to find the specified registry key or value.';
 }
 registryReplies.push({command:String(command),args:argv,hive,exit_code:error?error.code:0,stdout,stderr});
 queueMicrotask(()=>callback(error,stdout,stderr));
 return new EventEmitter();
};
Object.defineProperty(cp.execFile,promisify.custom,{value:(...args)=>new Promise((resolve,reject)=>cp.execFile(...args,(error,stdout,stderr)=>error?reject(Object.assign(error,{stdout,stderr})):resolve({stdout,stderr})))});
syncBuiltinESMExports();
const {inspectStartupSettings}=await import(input.worker??'../bridges/claude/dist/settings_preflight.js');
const report=await inspectStartupSettings({cwd:input.cwd,additional_files:[input.user,input.project,input.local,input.custom]});
const hash=p=>createHash('sha256').update(fs.readFileSync(p)).digest('hex');
const invalidJSON=['registry-malformed','registry-array','registry-null','registry-hkcu-malformed'].includes(input.mode);
const invalidValue=['registry-empty-success','registry-empty-value','registry-missing-value','registry-wrong-type'].includes(input.mode);
const checks={
 resolver_returned:report.resolver_returned,
 never_claims_complete:report.complete===false,
 private_values_not_exported:!JSON.stringify(report).includes('PRIVATE_PREFLIGHT_SENTINEL'),
 no_native_query: calls.every(c=>c.toLowerCase().endsWith('reg.exe')),
 user_raw_bytes_observed:report.files.some(f=>f.path===input.user&&f.sha256===hash(input.user)),
 custom_flag_file_read_separately:report.files.some(f=>f.path===input.custom&&f.sha256===hash(input.custom))&&!report.sources.some(s=>s.path===input.custom),
 missing_local_explicit:report.files.some(f=>f.path===input.local&&f.status==='absent'),
 read_status:report.read_complete===(!mutatingManaged&&!managedIOError&&!invalidJSON&&!invalidValue&&!['malformed','read-error','registry-denied','oversized','source-disappears'].includes(input.mode)),
};
if(input.mode.includes('binding-')){
 const conflicts=report.startup_conflicts;
 const expected=input.mode==='binding-benign'?'':input.mode.includes('invalid-env')?'invalid_environment':input.mode==='binding-state'?'state_environment':input.mode==='binding-api-helper'?'api_key_helper':'route_environment';
 checks.conflicts_reported=Array.isArray(conflicts)&&(!expected?conflicts.length===0:conflicts.some(c=>c.kind===expected));
 checks.no_helper_execution=!fs.existsSync(input.helper_marker);
 if(input.mode.startsWith('registry-binding-'))checks.registry_conflict_observed=Array.isArray(conflicts)&&conflicts.some(c=>c.source==='registry'&&c.kind===expected&&(input.mode!=='registry-binding-hkcu'||c.policy_origin==='hkcu'));
 else if(expected)checks.file_conflict_observed=Array.isArray(conflicts)&&conflicts.some(c=>c.source==='file'&&c.path===policyPath&&c.kind===expected);
}
if(managed){
 checks.managed_read_observed=report.files.some(f=>policyAliases.has(f.path)&&(input.mode==='managed-disappears'?f.status==='changed_during_read':f.sha256===createHash('sha256').update(policyBytes).digest('hex')));
 if(managedIOError){
  checks.owned_resolution_failure_reached=realpathFailureInjected;
  checks.resolution_failure_not_hidden=report.issues.some(i=>policyAliases.has(i.path)&&i.code==='EIO')&&!report.read_complete;
 }else if(mutatingManaged){
  checks.owned_mutation_reached=mutated&&fileChange.mutations.length===1;
  checks.changed_source_not_stable=report.files.some(f=>policyAliases.has(f.path)&&f.status==='changed_during_read');
 }else{
  checks.stable_managed_source_read=report.files.some(f=>policyAliases.has(f.path)&&f.status==='read')&&report.sources.some(s=>s.source==='managed'&&s.policy_origin==='file');
 }
 fileChange.after=identity(policyPath);
 fileChange.realpath_failure_injected=realpathFailureInjected;
 fs.writeFileSync(join(input.root,'file-change.json'),JSON.stringify(fileChange,null,2));
}
if(input.mode.startsWith('registry-')&&input.mode!=='registry-denied'){
 checks.registry_queries_succeeded=registryReplies.length===2&&registryReplies.every(r=>r.exit_code===0);
 if(invalidJSON||invalidValue){
  const invalid=report.registry.filter(r=>input.mode!=='registry-hkcu-malformed'||r.hive==='hkcu');
  checks.invalid_registry_not_read=invalid.length===(input.mode==='registry-hkcu-malformed'?1:2)&&invalid.every(r=>r.outcome===(invalidJSON?'invalid_json':'invalid_value'));
 }else{
  checks.registry_objects_read=report.registry.length===2&&report.registry.every(r=>r.outcome==='present');
 }
 if(['registry-valid','registry-expand','registry-hkcu-valid'].includes(input.mode)){
  checks.real_sdk_registry_source=report.sources.some(s=>s.policy_origin===(input.mode==='registry-hkcu-valid'?'hkcu':'hklm'));
 }
}
if(input.mode==='malformed')checks.invalid_json_not_hidden=report.files.some(f=>f.path===input.project&&f.status==='invalid_json')&&!report.sources.some(s=>s.path===input.project);
if(input.mode==='read-error')checks.read_failure_survives_close=report.issues.some(i=>i.path===input.project&&i.code==='EIO')&&!report.read_complete;
if(input.mode==='registry-denied')checks.registry_denial_remains_unknown=report.registry.length===2&&report.registry.every(r=>r.outcome==='unavailable');
if(input.mode==='oversized')checks.limit_not_partial_success=report.files.some(f=>f.path===input.project&&['limit_exceeded','unavailable'].includes(f.status));
if(input.mode==='source-disappears')checks.disappearance_is_not_clean_absence=report.files.some(f=>f.path===input.project&&f.status==='changed_during_read');
if(input.mode==='helper'||input.mode.startsWith('helpers-')){
 const {resolveSettings}=await import('../bridges/claude/node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs');
 const resolved=await resolveSettings({cwd:input.cwd,settingSources:['user','project','local']});
 const keys=settings=>Object.keys(settings).filter(k=>k==='policyHelper'||k==='policyHelpers');
 const raw={effective_keys:keys(resolved.effective),sources:resolved.sources.map(s=>({source:s.source,policy_origin:s.policyOrigin,helper_keys:keys(s.settings)}))};
 fs.writeFileSync(join(input.root,'policy-resolution.json'),JSON.stringify(raw,null,2));
 const singular=['helper','helpers-with-singular'].includes(input.mode);
 const plural=input.mode.startsWith('helpers-')&&input.mode!=='helpers-null';
 checks.sdk_retains_helper_declaration=input.mode==='helpers-null'||raw.sources.some(s=>s.source==='managed'&&s.helper_keys.includes(input.mode==='helper'?'policyHelper':'policyHelpers'));
 checks.no_helper_executed=!fs.existsSync(input.helper_marker);
 checks.singular_declaration_reported=report.policy_helper_present===singular;
 checks.platform_declaration_reported=report.policy_helpers_present===plural;
 checks.singular_gap_explicit=report.limitations.includes('policy_helper_not_executed')===singular;
 checks.platform_gap_explicit=report.limitations.includes('policy_helpers_not_evaluated')===plural;
 checks.singular_source_reported=report.sources.some(s=>s.source==='managed'&&s.has_policy_helper)===singular;
 checks.platform_source_reported=report.sources.some(s=>s.source==='managed'&&s.has_policy_helpers)===plural;
}
fs.writeFileSync(join(input.root,'registry-replies.json'),JSON.stringify(registryReplies,null,2));
fs.writeFileSync(join(input.root,'result.json'),JSON.stringify({checks,report,mode:input.mode,passed:Object.values(checks).every(Boolean),scope:'real SDK and production metadata worker; controlled WSL registry and system-policy filesystem; no production policy or native query execution'},null,2));
if(!Object.values(checks).every(Boolean))process.exitCode=1;
