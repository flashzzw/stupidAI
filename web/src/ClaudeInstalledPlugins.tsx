import { useState } from 'react';
import { ClaudeStartupSettings } from './ClaudeStartupSettings';
import { ClaudePluginSettingsPreview } from './ClaudePluginSettingsPreview';

type Entry = {
  source_key: string; name: string; scope: string; project_path?: string;
  registered_version: string; cache_path: string; cache_resolved_path?: string; cache_status: string;
  market_kind: string; market_path?: string; market_install_path?: string;
  declared_path?: string; resolved_path?: string; manifest_version?: string;
  status: string; detail: string;
};
type Catalog = {
  native_id: string; cwd: string; profile: string; snapshot_id: string; source: string;
  at: string; registry_path: string; complete: boolean; entries: Entry[];
  source_version: string; source_files: { path: string; sha256?: string; kind: string }[];
  errors: { path: string; detail: string }[];
};
const scopes: Record<string, string> = { user: '用户', project: '项目', local: '项目本地' };
const states: Record<string, string> = { local_declaration: '已读取本地声明', version_mismatch: '登记与文件版本不同', conflict: '声明存在冲突', unsupported: '来源尚未展开', unavailable: '声明待核对' };
const caches: Record<string, string> = { directory: '目录存在', missing: '路径缺失', not_directory: '不是目录', unreadable: '无法读取' };

export function ClaudeInstalledPlugins({ sessionId, baseSnapshotId, hasPendingChange, busy, connected, read }: {
  sessionId: string; connected: boolean; read: <T>(path: string, body?: unknown) => Promise<T>;
  baseSnapshotId: string; hasPendingChange: boolean; busy: boolean;
}) {
  const [catalog, setCatalog] = useState<Catalog>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  async function sample() {
    setCatalog(undefined); setError(''); setLoading(true);
    try { setCatalog(await read<Catalog>(`/api/sessions/${encodeURIComponent(sessionId)}/plugins/installed`, {})); }
    catch (e) { setError((e as Error).message); }
    finally { setLoading(false); }
  }
  return <details className="skill-discovery"><summary>Claude 已安装插件登记</summary>
    <p className="hint">读取保存的配置目录中的安装登记和本地市场声明。登记范围不表示本会话启用；缓存路径、文件声明与原生实际加载分别核对。</p>
    <button disabled={!connected || loading} onClick={() => void sample()}>{loading ? '正在读取登记…' : '读取安装登记'}</button>
    <ClaudeStartupSettings sessionId={sessionId} connected={connected} read={read} />
    {!connected ? <p className="hint">工作台已断开，连接后可重新读取。</p> : null}
    {error ? <p className="error" role="alert">{error}</p> : null}
    {catalog ? <>
      <p role="status">{catalog.complete ? '支持范围内的登记采样完成' : '登记采样不完整'} · {catalog.entries.length} 条安装记录</p>
      <p className="mono">登记文件：{catalog.registry_path}</p>
      <details><summary>本次登记与声明的文件版本</summary>
        <small className="mono">来源版本 {catalog.source_version}</small>
        {(catalog.source_files ?? []).map(file => <p className="mono" key={file.path}>{file.path}<small>{file.kind === 'absent' ? '不存在' : file.sha256}</small></p>)}
      </details>
      <small>采样时间：{new Date(catalog.at).toLocaleString()}</small>
      {catalog.entries.map((entry, index) => <div className="snapshot-asset" key={`${entry.source_key}/${index}`}>
        <p>{entry.source_key} · {scopes[entry.scope] ?? entry.scope}登记</p>
        {entry.project_path ? <p className="mono">登记项目：{entry.project_path}</p> : null}
        <p>登记版本：{entry.registered_version}</p>
        <p className="mono">登记缓存：{entry.cache_path}</p>
        <small>{caches[entry.cache_status] ?? '待核对'}；不代表此会话从这里加载。</small>
        {entry.cache_resolved_path && entry.cache_resolved_path !== entry.cache_path ? <p className="mono">缓存解析路径：{entry.cache_resolved_path}</p> : null}
        <p>市场登记类型：{entry.market_kind}</p>
        {entry.market_path ? <p className="mono">市场来源目录：{entry.market_path}</p> : null}
        {entry.market_install_path && entry.market_install_path !== entry.market_path ? <p className="mono">市场安装目录：{entry.market_install_path}</p> : null}
        {entry.declared_path ? <p className="mono">市场声明的插件目录：{entry.declared_path}</p> : null}
        {entry.resolved_path && entry.resolved_path !== entry.declared_path ? <p className="mono">插件目录解析路径：{entry.resolved_path}</p> : null}
        {entry.manifest_version ? <p>当前文件声明版本：{entry.manifest_version}</p> : null}
        <p>{states[entry.status] ?? '待核对'}</p><small>{entry.detail}</small>
        <ClaudePluginSettingsPreview key={`${entry.source_key}/${catalog.source_version}`} sessionId={sessionId} sourceKey={entry.source_key} sourceVersion={catalog.source_version}
          baseSnapshotId={baseSnapshotId} canSave={!hasPendingChange && !busy && catalog.snapshot_id === baseSnapshotId}
          available={connected && !!catalog.source_version && catalog.errors.length === 0 && entry.status === 'local_declaration' &&
            (entry.scope === 'user' || entry.project_path === catalog.cwd) && catalog.entries.filter(e => e.source_key === entry.source_key).length === 1} read={read} />
      </div>)}
      {!catalog.entries.length ? <p className="hint">此次未取得安装记录；不代表没有其他原生、受管或动态插件来源。</p> : null}
      {catalog.errors.map((issue, index) => <p className="error" key={index}>{issue.detail}<small className="mono">{issue.path}</small></p>)}
      <p className="hint">安装登记读取只核对登记与声明；“启动设置来源读取”单独显示设置文件和系统策略的读取状态。两者均不安装或重载插件，也不授权启停。实际加载、hook 执行和工具调用请分别查看对应证据。</p>
    </> : null}
  </details>;
}
