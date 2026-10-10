import { useState } from 'react';

type Report = {
  at: string; read_complete: boolean; complete: boolean; policy_helper_present: boolean; policy_helpers_present?: boolean;
  files: { path: string; status: string; sha256?: string; bytes?: number; resolved_path?: string; identity_sha256?: string }[];
  path_bindings?: { path: string; resolved_path: string; identity_sha256: string }[];
  sources: { source: string; path?: string; policy_origin?: string; settings_sha256: string; has_policy_helper?: boolean; has_policy_helpers?: boolean }[];
  startup_conflicts?: { source: string; path?: string; policy_origin?: string; kind: string }[];
  registry: { hive: string; outcome: string; code?: string }[];
  issues: { path: string; operation: string; code: string }[];
  bound_local_files: { path: string; kind: string; sha256?: string }[];
};
const states: Record<string,string> = {read:'已读取',absent:'不存在',unavailable:'无法读取',invalid_json:'JSON 无效',changed_during_read:'读取期间变化',limit_exceeded:'超过读取上限'};
const sourceNames: Record<string,string> = {user:'用户设置',project:'项目设置',local:'本地设置',managed:'受管设置',flag:'启动参数设置'};
const registryStates: Record<string,string> = {present:'已读取来源',absent:'来源不存在',unavailable:'读取结果未知',invalid_json:'设置 JSON 无效',invalid_value:'未读到有效的 Settings 值'};
const conflictNames: Record<string,string> = {api_key_helper:'凭据辅助程序',route_environment:'路由或凭据环境覆盖',state_environment:'状态目录环境覆盖',invalid_environment:'环境变量格式无效'};
const conflictSources: Record<string,string> = {...sourceNames,file:'已读取文件',registry:'注册表',effective:'预解析合并结果'};

export function ClaudeStartupSettings({sessionId,connected,read}:{sessionId:string;connected:boolean;read:<T>(path:string,body?:unknown)=>Promise<T>}) {
  const [report,setReport]=useState<Report>(),[loading,setLoading]=useState(false),[error,setError]=useState('');
  async function sample(){
    setReport(undefined);setError('');setLoading(true);
    try{setReport(await read<Report>(`/api/sessions/${encodeURIComponent(sessionId)}/plugins/startup-settings`,{}));}
    catch(e){setError((e as Error).message);}finally{setLoading(false);}
  }
  return <details className="startup-settings"><summary>启动设置来源读取</summary>
    <p className="hint">核对当前文件和系统策略来源的读取状态。读取不启动 Claude 查询，不执行插件或策略辅助程序，也不改变本会话配置。</p>
    <button disabled={!connected||loading} onClick={()=>void sample()}>{loading?'正在读取设置来源…':'读取设置来源'}</button>
    {error?<p className="error" role="alert">{error}</p>:null}
    {report?<>
      <p role="status">{report.read_complete?'本次来源读取完成':'本次来源读取不完整'}；完整启动配置仍待核对。</p>
      <small>读取时间：{new Date(report.at).toLocaleString()}</small>
      {report.files.map(file=><div className="snapshot-asset" key={file.path}><p>{states[file.status]??'读取结果未知'}</p><p className="mono">{file.path}</p>{file.sha256?<small className="mono">文件内容标识 {file.sha256}</small>:null}</div>)}
      {report.path_bindings?.some(p=>p.path!==p.resolved_path)?<details><summary>本次路径解析</summary>{report.path_bindings.filter(p=>p.path!==p.resolved_path).map(p=><div className="snapshot-asset" key={p.path}><p className="mono">{p.path}</p><p className="mono">解析到 {p.resolved_path}</p></div>)}</details>:null}
      {report.registry.length?<div className="snapshot-asset"><p>Windows 策略来源</p>{report.registry.map((r,i)=><p key={i}>{r.hive.toUpperCase()} · {registryStates[r.outcome]??'读取结果未知'}{r.code?`（返回码 ${r.code}）`:''}</p>)}</div>:null}
      {report.sources.length?<details><summary>预解析报告的来源层</summary>{report.sources.map((s,i)=><div className="snapshot-asset" key={i}><p>{sourceNames[s.source]??s.source}{s.policy_origin?` · ${s.policy_origin}`:''}</p>{s.path?<p className="mono">{s.path}</p>:null}<small className="mono">来源设置标识 {s.settings_sha256}</small></div>)}</details>:null}
      <details><summary>原生 Git/cwd 对应的本地文件</summary>{report.bound_local_files.map(f=><p className="mono" key={f.path}>{f.path} · {f.kind==='absent'?'不存在':'已核对文件版本'}</p>)}</details>
      {report.issues.map((issue,i)=><p className="error" key={i}>来源读取异常：{issue.code}<small className="mono">{issue.path}</small></p>)}
      {report.startup_conflicts?.length?<div className="startup-binding-conflicts"><p className="error">来源中发现启动绑定冲突，设置校验与插件设置预览暂不可用。</p>{report.startup_conflicts.map((c,i)=><div className="snapshot-asset" key={i}><p>{conflictNames[c.kind]??'未知绑定冲突'} · {conflictSources[c.source]??c.source}{c.policy_origin?` · ${c.policy_origin}`:''}</p>{c.path?<p className="mono">{c.path}</p>:null}</div>)}<p className="hint">这里只报告已观察到的声明；未运行辅助程序、未重写来源，也不表示所有来源都已核对。</p></div>:null}
      {report.policy_helper_present||report.sources.some(s=>s.has_policy_helper)?<p className="hint">来源声明了策略辅助程序，但本次没有执行；其最终策略仍未知。</p>:null}
      {report.policy_helpers_present||report.sources.some(s=>s.has_policy_helpers)?<p className="hint">来源声明了按平台选择的策略辅助配置（policyHelpers）；本次没有执行程序或应用备用策略。最终策略仍未知，设置校验与插件设置预览暂不可用。</p>:null}
      <p className="hint">预解析与原生 Git/cwd 来源可能不同；本次未获取最新服务端策略，不能据此确认完整权限或允许已安装插件启停。文件正文与策略值不在此展示。</p>
    </>:null}
  </details>;
}
