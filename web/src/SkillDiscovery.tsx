import { useState } from 'react';

type SkillInventory = {
  cwd: string; source: string; at: string; complete: boolean;
  skills: { name: string; description: string; path: string; scope: string; enabled: boolean; plugin_id?: string }[];
  errors: { path?: string; detail: string }[];
  snapshot_id: string;
  controls: { path: string; control: string; detail: string }[];
  saved_selections: { asset_id: string; version: string; name: string; path: string; mode: string; discovered: boolean }[];
};
const scopes: Record<string, string> = { user: '用户', repo: '项目', system: '系统内置', admin: '管理员受管' };
const controls: Record<string, string> = { native_directory: '普通目录入口', plugin_bundle: '按插件整包控制', unsupported: '暂无独立控制入口', ambiguous: '来源有歧义', unknown: '控制范围待核对' };

export function SkillDiscovery({ sessionId, connected, read }: {
  sessionId: string; connected: boolean; read: <T>(path: string, body?: unknown) => Promise<T>;
}) {
  const [result, setResult] = useState<SkillInventory>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const controlByPath = new Map(result?.controls?.map(item => [item.path, item]));
  async function discover() {
    setLoading(true); setError(''); setResult(undefined);
    try { setResult(await read<SkillInventory>(`/api/sessions/${encodeURIComponent(sessionId)}/skills/discover`, {})); }
    catch (e) { setError((e as Error).message); }
    finally { setLoading(false); }
  }
  return <details className="skill-discovery"><summary>Skill 来源发现</summary>
    <p className="hint">查询此执行配置与工作目录当前可发现的 Skill。启停状态来自目录配置，尚未核对本会话是否加载或使用；方法是否生效仍需任务和 Gate 验证。</p>
    <button disabled={!connected || loading} onClick={() => void discover()}>{loading ? '正在查询 Skill…' : '刷新 Skill 来源'}</button>
    {!connected ? <p className="hint">工作台已断开，连接后可重新查询。</p> : null}
    {error ? <p className="error" role="alert">{error}</p> : null}
    {result ? <>
      <p role="status">{result.complete ? `原生扫描完成 · ${result.skills.length} 项` : '原生扫描不完整 · 部分来源待核对'}</p>
      <p className="mono">查询目录：{result.cwd}</p>
      <small>查询时间：{new Date(result.at).toLocaleString()}</small>
      {result.saved_selections?.length ? <section aria-label="发现查询所依据的保存选择">
        <h4>本会话已保存的原生目录选择</h4>
        <p className="hint">以下版本来自保存快照，路径出现在清单中只表示可发现。当前是否生效请查看执行核对结果；源文件变化不会升级这些版本。</p>
        <p className="mono">快照：{result.snapshot_id}</p>
        {result.saved_selections.map(saved => <div className="snapshot-asset" key={saved.asset_id}>
          <p>{saved.name} · 已保存{saved.mode === 'enabled' ? '启用' : '禁用'}</p>
          <small>资产 {saved.asset_id} · 版本 {saved.version}</small>
          <p className="mono">{saved.path}</p>
          <p className="hint">{saved.discovered ? '本次清单返回此路径；完整资源与当前加载状态未由发现操作核对。' : '本次清单未返回此路径；保存选择仍然保留，不代表已撤销或已恢复继承。'}</p>
        </div>)}
      </section> : null}
      {result.skills.map(skill => <div className="snapshot-asset" key={skill.path}>
        <p>{skill.name} · 可发现 · {skill.enabled ? '目录配置启用' : '目录配置禁用'}</p>
        <p>{skill.description}</p>
        <small>来源：{scopes[skill.scope] ?? `未知类别（${skill.scope}）`}{skill.plugin_id ? ` · CLI 插件 ${skill.plugin_id}` : ''}</small>
        <p className="mono">{skill.path}</p>
        <p>{controls[controlByPath.get(skill.path)?.control ?? 'unknown'] ?? '控制范围待核对'}</p>
        <p className="hint">{controlByPath.get(skill.path)?.detail ?? '本次结果未提供控制范围，请重新查询。'}</p>
      </div>)}
      {!result.skills.length ? <p className="hint">本次扫描没有返回可展示的 Skill；不代表历史上下文中没有方法指引。</p> : null}
      {result.errors.map((item, index) => <div className="error" key={`${item.path}/${index}`}><p>{item.detail}</p>{item.path ? <p className="mono">{item.path}</p> : null}</div>)}
      <p className="hint">发现只提供目录采样与保存选择的对照，不会变更配置或解除输入限制。完整资源与实际选择须在登记、改配和执行时另行核对；禁用不会抹除历史内容或改变文件读取权限。</p>
    </> : null}
  </details>;
}
