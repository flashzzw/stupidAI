import { useEffect, useRef, useState } from 'react';
import type { CommandFields } from './PendingAction';
import type { ConfigurationFailure } from './ConfigurationFailureDetails';

type RecoveryCheck = {
  base_snapshot_id:string; pending_change_id:string; satisfied:boolean; coverage:string;
  installed_settings?:{source_version:string;next_revision:number;restored_files:string[]};
  original_query_retry?:boolean;
  native:{version:number; native_id:string; operation:string; baseline:string; at:string; source:string; read_only:boolean; checks:{kind:string;status:string}[];installed_recovery?:{startup_source_version:string};original_query_retry?:{version:number;failure:ConfigurationFailure}};
  sources:{name:string;path:string;status:string;detail:string}[];
};
const claudeLabels:Record<string,string>={transaction:'原失败事务与绑定',owned_query_stopped:'自有查询已退出',work_clear:'没有未决工作',original_history:'原生历史与原基线一致',original_settings:'原设置与安装来源版本',read_stable:'读取期间依据保持一致'};

export function ExecutionRecoveryDetails({sessionId,base,pending,operation,baseline,connected,busy,read,command,backend="claude",failure}:{
  backend?:string;sessionId:string;base:string;pending:string;operation:string;baseline:string;connected:boolean;busy:boolean;
  failure?:ConfigurationFailure;
  read:<T>(path:string,body?:unknown)=>Promise<T>;command:(fields:CommandFields)=>Promise<void>;
}) {
  const labels:Record<string,string>=backend==='codex'?{transaction:'原改配事务与绑定',thread_unloaded:'原线程与加载清单均确认未加载',work_clear:'没有未决工作',original_history:'原生历史与原基线一致',original_configuration:'原配置来源与约束可核对',read_stable:'读取期间依据保持一致'}:claudeLabels;
  const coverage=backend==='codex'?'native_thread_and_recorded_sources':'owned_query_and_recorded_sources';
  const [result,setResult]=useState<RecoveryCheck>(),[error,setError]=useState(''),[loading,setLoading]=useState(false),generation=useRef(0);
  useEffect(()=>{generation.current++;setResult(undefined);setError('');setLoading(false);return()=>{generation.current++;};},[sessionId,base,pending,operation,baseline,connected,busy,backend,failure?.version,failure?.operation,failure?.baseline,failure?.at,failure?.stage,failure?.reason,failure?.cleanup]);
  async function inspect(){
    const current=++generation.current;setLoading(true);setResult(undefined);setError('');
    try{
      const out=await read<RecoveryCheck>(`/api/sessions/${encodeURIComponent(sessionId)}/execution/recovery-check`,{base_snapshot_id:base,pending_change_id:pending,operation_id:operation});
      if(current!==generation.current)return;
      const conditions=out.native?.checks;
      if(out.base_snapshot_id!==base||out.pending_change_id!==pending||out.coverage!==coverage||out.native?.operation!==operation||out.native.baseline!==baseline||out.native.version!==1||!out.native.read_only||out.native.source!==backend+'.configuration_recovery'||!Array.isArray(conditions)||conditions.length!==Object.keys(labels).length||new Set(conditions.map(c=>c.kind)).size!==conditions.length||conditions.some(c=>!Object.hasOwn(labels,c.kind)||!['passed','blocked','unavailable'].includes(c.status))||!Array.isArray(out.sources)||out.satisfied!==(conditions.every(c=>c.status==='passed')&&out.sources.every(s=>s.status==='unchanged')))throw Error('恢复核对结果与当前候选不一致，请重新核对。');
      const installed=out.installed_settings;
      if(Boolean(installed)!==Boolean(out.native.installed_recovery)||installed&&(backend!=='claude'||!/^[a-f0-9]{64}$/.test(installed.source_version)||installed.source_version!==out.native.installed_recovery?.startup_source_version||!Number.isInteger(installed.next_revision)||installed.next_revision<2||!Array.isArray(installed.restored_files)||installed.restored_files.some(path=>typeof path!=='string')))throw Error('恢复文件身份的版本依据不完整，请重新核对。');
      const retry=out.native.original_query_retry, original=retry?.failure;
      if(out.original_query_retry!==undefined&&out.original_query_retry!==true||Boolean(out.original_query_retry)!==Boolean(retry)||retry&&(backend!=='claude'||retry.version!==1||!original||original.version!==1||original.operation!==operation||original.baseline!==baseline||!['open_original','check_original_components'].includes(original.stage)||original.reason!=='stage_failed'||original.cleanup!=='confirmed'||!Number.isFinite(Date.parse(original.at))||!failure||['version','operation','baseline','stage','reason','cleanup','at'].some(key=>original[key as keyof ConfigurationFailure]!==failure[key as keyof ConfigurationFailure])))throw Error('候选启动前的恢复依据与原失败不一致，请重新核对。');
      setResult(out);
    }catch(e){if(current===generation.current)setError((e as Error).message);}
    finally{if(current===generation.current)setLoading(false);}
  }
  return <section aria-label="恢复前只读核对">
    <button disabled={!connected||busy||loading} onClick={()=>void inspect()}>{loading?'正在核对恢复条件…':'核对恢复条件'}</button>
    {result?<div role="status">
      <p>{result.satisfied?'已列出的条件通过，仍保持暂停。':'还有条件未满足，保持待核对。'}</p>
      {result.native.checks.map(c=><p key={c.kind}>{labels[c.kind]}：{c.status==='passed'?'已核对':c.status==='blocked'?'未满足':'无法核对'}</p>)}
      {result.sources.every(s=>s.status==='unchanged')?<p>已记录的原配置来源均匹配。</p>:result.sources.filter(s=>s.status!=='unchanged').map(s=><div key={`${s.name}/${s.path}`}><p>{s.name}：{s.detail}</p>{s.detail.includes(s.path)?null:<small className="mono">{s.path}</small>}</div>)}
      {result.installed_settings?<section aria-label="恢复文件身份版本"><p>恢复将创建配置版本 {result.installed_settings.next_revision}，记录还原后本地文件的新身份。配置内容与原版本一致，旧快照进入历史，失败尝试继续保留。</p>{result.installed_settings.restored_files.map(path=><small className="mono" key={path}>{path}</small>)}<small className="mono">恢复来源版本 {result.installed_settings.source_version}</small></section>:null}
      {result.original_query_retry?<section aria-label="候选启动前的恢复"><p>候选尚未启动。将按原 ID 重开原配置并核对能力，保留原失败记录。</p><p>本次恢复没有可比较的失败前能力清单，将单独记录首次成功核对的结果。</p></section>:null}
      {result.satisfied?<button disabled={!connected||busy||loading} onClick={()=>{setResult(undefined);void command({kind:"recover_execution",session_id:sessionId,execution_recovery:{base_snapshot_id:base,pending_change_id:pending,operation_id:operation,...(result.installed_settings?{installed_source_version:result.installed_settings.source_version}:{}),...(result.original_query_retry?{original_query_retry:true}:{})}});}}>{backend==='codex'?'按原配置恢复原线程':result.original_query_retry?'按原配置重开并核对能力':'按原配置恢复查询'}</button>:null}
      <small>核对时间：{new Date(result.native.at).toLocaleString()}</small>
    </div>:null}
    {error?<p role="alert" className="error">{error}</p>:null}
    <p className="hint">{backend==='codex'?'核对不会加载线程。仅在原生确认线程未加载、历史未变且无未决工作时，明确恢复才按原 ID 建立原配置和权限。保留失败候选，自动化仍暂停；回执未知不重复恢复，不关闭共享后台。':'核对不会启动查询、改变快照。明确恢复会按原 ID 重开原配置，启动 hooks 可能再次执行；失败候选和原因保留，自动化仍暂停，已发生的副作用不会撤销。'}</p>
  </section>;
}
