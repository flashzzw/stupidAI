import { useState } from 'react';
import { ClaudeSkillDirectories, type DirectorySample } from './ClaudeSkillDirectories';

type Catalog = {
  native_id: string; cwd: string; source: string; at: string; complete: boolean;
  items: { name: string; description: string; source: string; plugin_name?: string; aliases: string[];
    command_advertised: boolean; description_listed: boolean; builtin: boolean; ambiguous: boolean;
    control: string; detail: string; asset_id?: string; bound_path?: string; configured_mode?: string }[];
  limitations: string[];
  directories?: DirectorySample;
  instructions?: { available: boolean; items: { path: string; source: string; tokens: number }[] };
};
const sources: Record<string,string> = {userSettings:'用户',projectSettings:'项目',localSettings:'项目本地',policySettings:'管理员受管',plugin:'CLI 插件',builtin:'CLI 内置命令','built-in':'CLI 内置 Skill',unknown:'原生未报告'};
const controls: Record<string,string> = {asset_bound:'已关联保存选择',requires_binding:'需先登记并核对入口',unavailable:'尚不能独立选配',ambiguous:'来源或名称冲突'};
const instructionSources: Record<string,string> = {User:'用户',Project:'项目（含上层目录）',Local:'项目本地',Managed:'管理员受管'};

export function ClaudeSkillCatalog({ sessionId, connected, read }: {
  sessionId: string; connected: boolean; read: <T>(path:string,body?:unknown)=>Promise<T>;
}) {
  const [catalog,setCatalog]=useState<Catalog>();
  const [loading,setLoading]=useState(false);
  const [error,setError]=useState('');
  async function sample() {
    setCatalog(undefined);setError('');setLoading(true);
    try {setCatalog(await read<Catalog>(`/api/sessions/${encodeURIComponent(sessionId)}/skills/catalog`,{}));}
    catch(e){setError((e as Error).message);}
    finally{setLoading(false);}
  }
  const renderItem=(item:Catalog['items'][number],index:number)=><div className="snapshot-asset" key={`${item.name}/${item.source}/${item.plugin_name??''}/${index}`}>
        <p>{item.name} · {item.description_listed?'Skill 描述已报告':'仅命令清单条目'}</p>
        {item.description?<p>{item.description}</p>:null}
        <small>来源：{sources[item.source]??`未知类别（${item.source}）`}{item.plugin_name?` · ${item.plugin_name}`:''}</small>
        <p>命令清单：{item.command_advertised?'包含':'未报告'} · 描述清单：{item.description_listed?'包含':'未报告'}</p>
        {item.aliases.length?<p className="mono">别名：{item.aliases.join('、')}</p>:null}
        <p>{controls[item.control]??'选配能力待核对'}</p><small>{item.detail}</small>
        {item.asset_id?<p>保存的资产：{item.asset_id} · {item.configured_mode==='enabled'?'启用':'禁用'}</p>:null}
        {item.bound_path?<p className="mono">快照绑定入口：{item.bound_path}</p>:null}
      </div>;
  return <details className="skill-discovery"><summary>Claude Skill 来源清单</summary>
    <p className="hint">读取当前已开启查询的命令、别名、Skill 描述与指令文件来源，并单列保存配置下的用户/项目目录入口。读取不会启动或恢复查询，也不应用配置。</p>
    <button disabled={!connected||loading} onClick={()=>void sample()}>{loading?'正在读取来源…':'读取当前原生清单'}</button>
    {!connected?<p className="hint">工作台已断开，连接后可重新读取。</p>:null}
    {error?<p className="error" role="alert">{error}</p>:null}
    {catalog?<>
      <p role="status">当前查询报告 · {catalog.items.length} 项</p>
      <p className="mono">原生会话：{catalog.native_id}</p><small>读取时间：{new Date(catalog.at).toLocaleString()}</small>
      <section aria-label="当前查询的指令文件">
        <h4>当前查询的指令文件</h4>
        {catalog.instructions?.available ? <>
          <p>原生报告了 {catalog.instructions.items.length} 个指令文件。</p>
          {catalog.instructions.items.map(item=><div className="snapshot-asset" key={`${item.path}/${item.source}`}>
            <p className="mono">{item.path}</p>
            <small>来源：{instructionSources[item.source]??item.source} · 约 {item.tokens.toLocaleString()} tokens</small>
          </div>)}
          {!catalog.instructions.items.length?<p className="hint">本次报告未列出指令文件；不能据此判断没有其他来源。</p>:null}
        </> : <p className="hint">原生查询未提供可核对的指令文件清单。</p>}
        <p className="hint">这些文件由 CLI 继承。路径和 token 估算来自本次原生报告，未绑定已加载的文件版本；完整来源、独立排除与权限覆盖仍需另外核对。</p>
      </section>
      {catalog.items.filter(item=>item.description_listed&&item.source!=='built-in').map(renderItem)}
      {catalog.items.some(item=>!item.description_listed||item.source==='built-in')?<details><summary>内置 Skill 与仅命令条目 · {catalog.items.filter(item=>!item.description_listed||item.source==='built-in').length} 项</summary>
        {catalog.items.filter(item=>!item.description_listed||item.source==='built-in').map(renderItem)}
      </details>:null}
      {!catalog.items.length?<p className="hint">当前查询未报告条目；这不能证明没有其他隐藏来源。</p>:null}
      {catalog.directories ? <ClaudeSkillDirectories sample={catalog.directories} /> : null}
      {catalog.limitations.map(text=><p className="hint" key={text}>{text}</p>)}
    </>:null}
  </details>;
}
