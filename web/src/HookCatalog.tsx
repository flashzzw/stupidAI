import { useState } from 'react';

type HookCatalogData = {
  native_id: string; cwd: string; at: string; warning_count?: number; errors?: {path?: string; detail: string}[]; items: {
    key?: string; source_path?: string; plugin_id?: string; trust_status?: string; managed?: boolean;
    event: string; matcher: string; source: string; plugin_name: string; type: string;
    disabled: boolean; runs_once: boolean; background: boolean; conditional: boolean;
    timeout_seconds?: number; definition_hash: string;
  }[];
  policy: { disabled_by_policy: boolean; managed_only: boolean; plugin_only: boolean; all_disabled: boolean;
    policy_hook_count: number; policy_unreadable: boolean; safe_mode: boolean; bare_mode: boolean; settings_error_count: number };
  limitations: string[];
};

const sources: Record<string, string> = { userSettings: '用户', projectSettings: '项目', localSettings: '项目本地',
  policySettings: '管理员受管', flagSettings: '当前查询设置', sessionHook: '会话', pluginHook: 'CLI 插件',
  system: '系统', user: '用户', project: '项目', mdm: '设备管理', sessionFlags: '会话参数', plugin: 'CLI 插件',
  cloudRequirements: '云端要求', cloudManagedConfig: '云端受管配置', legacyManagedConfigFile: '受管配置文件', legacyManagedConfigMdm: '受管设备配置', unknown: '未知来源' };
const trust: Record<string,string> = { managed:'受管', trusted:'已信任', untrusted:'未信任', modified:'定义已变化' };

export function HookCatalog({ sessionId, backend, connected, read }: {
  sessionId: string; backend:'codex'|'claude'; connected: boolean; read: <T>(path: string, body?: unknown) => Promise<T>;
}) {
  const codex=backend==='codex';
  const [catalog, setCatalog] = useState<HookCatalogData>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  async function sample() {
    setCatalog(undefined); setError(''); setLoading(true);
    try { setCatalog(await read<HookCatalogData>(`/api/sessions/${encodeURIComponent(sessionId)}/hooks/catalog`, {})); }
    catch (error) { setError((error as Error).message); }
    finally { setLoading(false); }
  }
  return <details className="skill-discovery hook-catalog"><summary>{codex?'Codex':'Claude'} hook 来源清单</summary>
    <p className="hint">{codex?'按保存的工作目录与执行配置发现 hooks、来源及信任状态；不代表本会话已加载。读取不会执行 hook、授予信任或启动线程。':'查看当前查询登记的自动化 hooks、来源和禁用状态。读取不会执行 hook、重载插件或启动查询。'}</p>
    <button disabled={!connected || loading} onClick={() => void sample()}>{loading ? '正在读取 hooks…' : '读取当前 hooks'}</button>
    {!connected ? <p className="hint">工作台已断开，连接后可重新读取。</p> : null}
    {error ? <p className="error" role="alert">{error}</p> : null}
    {catalog ? <>
      <p role="status">{codex?'按工作目录发现':'当前查询报告'} · {catalog.items.length} 项 hook · 覆盖不完整</p>
      <p className="mono">{codex?'查询关联会话':'原生会话'}：{catalog.native_id}</p>
      {codex?<p className="mono">工作目录：{catalog.cwd}</p>:null}<small>读取时间：{new Date(catalog.at).toLocaleString()}</small>
      {catalog.warning_count ? <p>原生报告 {catalog.warning_count} 条提示；此处省略可能包含源内容的原始正文。</p> : null}
      {catalog.errors?.map((error,index)=><p className="error" key={index}>{error.detail}{error.path?<small className="mono">{error.path}</small>:null}</p>)}
      {!codex ? <>
      {catalog.policy.all_disabled ? <p>原生设置：关闭 hooks；受管 hook 仍需按策略单独核对。</p> : null}
      {catalog.policy.disabled_by_policy ? <p>管理员策略：关闭 hooks。</p> : null}
      {catalog.policy.managed_only ? <p>管理员策略：仅允许受管 hooks；受管条目可能不在清单中。</p> : null}
      {catalog.policy.plugin_only ? <p>管理员策略：仅允许插件自定义；工作台没有修改入口。</p> : null}
      {catalog.policy.policy_hook_count > 0 ? <p>原生报告受管 hooks：{catalog.policy.policy_hook_count} 项。</p> : null}
      {catalog.policy.policy_unreadable ? <p className="error">受管策略无法读取，实际限制未知。</p> : null}
      {catalog.policy.safe_mode ? <p>原生安全模式开启；受管 hooks 仍可能适用。</p> : null}
      {catalog.policy.bare_mode ? <p>原生精简模式开启；会话 hooks 与配置来源 hooks 的规则不同。</p> : null}
      {catalog.policy.settings_error_count > 0 ? <p className="error">原生报告 {catalog.policy.settings_error_count} 项配置解析错误；被跳过来源不在有效清单中。</p> : null}
      </> : null}
      {catalog.items.map((item, index) => <div className="snapshot-asset" key={`${item.definition_hash}/${index}`}>
        <p>{item.event} · {item.type}</p>
        <small>来源：{sources[item.source] ?? `未知类别（${item.source}）`}{item.plugin_name ? ` · ${item.plugin_name}` : ''}{item.plugin_id ? ` · ${item.plugin_id}` : ''}</small>
        {item.source_path?<p className="mono">原生来源路径：{item.source_path}</p>:null}
        <p>{item.disabled ? '原生报告已禁用' : codex?'已发现 · 执行结果未核对':'已登记 · 执行结果未核对'}</p>
        {item.trust_status?<p>信任状态：{trust[item.trust_status] ?? `未知状态（${item.trust_status}）`} · 不代表执行成功</p>:null}
        {item.managed===true?<p>原生报告为受管 hook</p>:null}
        <p className="mono">匹配范围：{item.matcher || '无匹配条件'}</p>
        {item.conditional ? <p>含额外执行条件</p> : null}
        {item.runs_once ? <p>仅执行一次</p> : null}
        {item.background ? <p>后台执行</p> : null}
        {item.timeout_seconds !== undefined ? <p>超时：{item.timeout_seconds} 秒</p> : null}
        <details><summary>定义摘要</summary>{item.key?<p className="mono">原生键：{item.key}</p>:null}<p className="mono">{item.definition_hash}</p></details>
      </div>)}
      {!catalog.items.length ? <p className="hint">{codex?'本次未发现 hook；这不能证明不存在被信任规则或策略过滤的来源。':'当前菜单没有报告 hook；这不能证明不存在隐藏或被跳过来源。'}</p> : null}
      {catalog.limitations.map(text => <p className="hint" key={text}>{text}</p>)}
    </> : null}
  </details>;
}
