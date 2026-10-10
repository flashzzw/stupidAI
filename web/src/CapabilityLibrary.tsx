import { useEffect, useRef, useState, type FormEvent } from 'react';
import type { CapabilityAsset } from './ExecutionDetails';
import { CapabilityDiagnosis, type Diagnosis } from './CapabilityDiagnosis';
import { CapabilityPackage } from './CapabilityPackage';
import { PluginHookInspection } from './PluginHookInspection';

type Stamp = { path: string; resolved_path: string; kind: string; sha256?: string };
type Use = { session_id: string; title: string; snapshot_id: string; version: string; phase: string; source: string };
type Version = { asset: CapabilityAsset; source?: Stamp; imported_from?: string; created_at: string };
type Item = { asset: CapabilityAsset; revision: string; removed: boolean; management: string; source?: Stamp; source_error?: string;
  source_state: string; compatibility: string; detail: string; diagnosis?: Diagnosis; same_name_with: string[]; uses: Use[]; versions: Version[] };
type Change = { action: string; asset_id: string; base_revision: string; asset?: CapabilityAsset; preview_hash?: string };
type Preview = { request: Change; preview_hash: string; current?: Item; candidate?: Item; uses: Use[]; boundary: string };
type Read = <T>(path: string, body?: unknown) => Promise<T>;
const kinds: Record<string, string> = { skill: 'Skill', mcp: 'MCP 定义', cli_plugin: 'CLI 插件' };
const phases: Record<string, string> = { current: '当前版本', history: '历史版本', pending: '待应用版本', failed_attempt: '未生效的配置尝试' };
const states: Record<string, string> = { readable: '来源可读', changed: '来源已变化', unavailable: '来源不可读', unverified: '尚未核对' };

export function CapabilityLibrary({ catalogKey, connected, read, selectSession, instances }: {
  catalogKey: string; connected: boolean; read: Read; selectSession: (id: string) => void;
  instances: { id: string; name?: string; kind: string }[];
}) {
  const [items, setItems] = useState<Item[]>([]);
  const [filter, setFilter] = useState('all');
  const [revision, setRevision] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [editing, setEditing] = useState<{ action: string; item?: Item }>();
  const [packaging, setPackaging] = useState<{ exporting?: { asset_id: string; revision: string; version: string } }>();
  useEffect(() => {
    let ignore = false;
    setLoading(true); setError('');
    read<Item[]>('/api/capabilities').then(value => { if (!ignore) setItems(value); })
      .catch(e => { if (!ignore) setError((e as Error).message); }).finally(() => { if (!ignore) setLoading(false); });
    return () => { ignore = true; };
  }, [catalogKey, read, revision]);
  const visible = items.filter(item => filter === 'all' || (filter === 'removed' ? item.removed : !item.removed &&
    (filter === 'shared' ? item.asset.backends.length === 2 : item.asset.backends.length === 1 && item.asset.backends[0] === filter)));
  return <section className="capability-library" aria-label="能力资产库">
    <div className="section-heading"><div><span className="eyebrow">CAPABILITY LIBRARY</span><h1>能力库</h1></div>
      <button className="primary" disabled={!connected} onClick={() => setEditing({ action: 'register' })}>登记或导入能力</button></div>
    <p className="hint">共享定义，各自执行。这里管理来源、版本与引用；原生安装、加载和使用分别核对。</p>
    <div className="library-toolbar"><label>能力分区<select value={filter} onChange={event => setFilter(event.target.value)}>
      <option value="all">全部资产</option><option value="shared">共享 Skill / MCP</option><option value="codex">Codex 专属</option><option value="claude">Claude 专属</option><option value="removed">已移除登记</option>
    </select></label><button disabled={!connected || loading} onClick={() => setRevision(n => n + 1)}>刷新来源与引用</button>
      <button disabled={!connected || loading} onClick={() => setPackaging({})}>导入资源包</button>
      <a className="library-export" href="/api/capabilities/export" download="capability-catalog.json">导出目录清单</a></div>
    <p className="hint">目录清单仅包含元数据与内容标识；不包含资源文件、凭据或会话，复用时需重新指定来源。</p>
    {loading ? <p role="status">正在读取能力来源…</p> : null}
    {notice ? <p role="status" className="hint">{notice}</p> : null}
    {error ? <p role="alert" className="error">{error}</p> : null}
    {!loading && !visible.length ? <p className="hint">此分区还没有能力资产。已有目录默认登记为外部管理。</p> : null}
    {visible.map(item => <article className="capability-card" key={item.asset.id}>
      <div className="section-heading"><h2>{item.asset.name}</h2><span className="badge">{item.removed ? '已移除登记' : item.asset.disabled ? '已停用新引用' : states[item.source_state]}</span></div>
      <p className="muted">{kinds[item.asset.kind]} · {item.asset.id} · {item.asset.version} · {item.asset.backends.join(' / ')}</p>
      <p>{item.management === 'managed' ? '平台管理 · 固定版本' : '外部管理 · 动态来源'}</p>
      <p className="mono">{item.asset.source}</p>{item.source ? <p className="mono">内容标识 {item.source.sha256}</p> : null}
      {item.source_error ? <p className="error">{item.source_error}</p> : null}
      {item.same_name_with.length ? <p className="warning">同名登记：{item.same_name_with.join('、')}。不同来源与 ID 保持独立，原生同名优先级尚未核对。</p> : null}
      <p className="hint">{item.detail}</p>
      <CapabilityDiagnosis value={item.diagnosis} />
      {!loading && !item.removed && item.asset.kind === 'cli_plugin' && item.asset.backends.includes('codex') ?
        <PluginHookInspection assetId={item.asset.id} version={item.asset.version} revision={item.revision} sourceHash={item.source?.sha256 ?? ''}
          instances={instances} connected={connected} read={read} /> : null}
      <div className="library-actions">
        <button disabled={!connected || item.removed} onClick={() => setEditing({ action: 'update', item })}>更新登记版本</button>
        <button disabled={!connected || item.removed} onClick={() => setEditing({ action: item.asset.disabled ? 'enable' : 'disable', item })}>{item.asset.disabled ? '恢复新引用' : '停用新引用'}</button>
        <button disabled={!connected || item.removed} onClick={() => setEditing({ action: 'remove', item })}>移除登记</button>
        <button disabled={!connected} onClick={() => setPackaging({ exporting: { asset_id: item.asset.id, revision: item.revision, version: item.asset.version } })}>导出资源包</button>
      </div>
      <details className="library-history"><summary>版本与引用 · {item.versions.length} 个版本 / {item.uses.length} 处引用</summary>
        {item.versions.map(version => <div className="library-version" key={version.asset.version}><p>{version.asset.version} · {version.created_at ? new Date(version.created_at).toLocaleString() : '启动配置登记'}</p>
          <p className="mono">{version.asset.source}</p>{version.imported_from ? <p className="mono">导入自：{version.imported_from}</p> : null}
          <button disabled={!connected} onClick={() => setPackaging({ exporting: { asset_id: item.asset.id, revision: item.revision, version: version.asset.version } })}>导出 {version.asset.version} 资源包</button>
          <small>历史来源会在恢复或输入前重新核对；版本记录不保证外部文件仍存在。</small></div>)}
        <Uses uses={item.uses} selectSession={selectSession} />
      </details>
    </article>)}
    {editing ? <CapabilityEditor action={editing.action} item={editing.item} read={read} connected={connected} close={() => setEditing(undefined)} saved={() => {
      setEditing(undefined); setRevision(n => n + 1); setNotice('能力登记已保存，已有执行和快照保持原绑定。');
    }} /> : null}
    {packaging ? <CapabilityPackage exporting={packaging.exporting} targets={items.filter(item => !item.removed).map(item => ({ id: item.asset.id, name: item.asset.name, revision: item.revision }))} read={read} connected={connected} close={() => setPackaging(undefined)} saved={() => {
      setPackaging(undefined); setRevision(n => n + 1); setNotice('资源包已导入固定版本，已有快照保持原绑定。');
    }} /> : null}
  </section>;
}

function Uses({ uses, selectSession }: { uses: Use[]; selectSession?: (id: string) => void }) {
  return <div className="capability-uses">{uses.length ? uses.map(use => <p key={`${use.session_id}/${use.snapshot_id}/${use.phase}`}>
    {selectSession ? <button onClick={() => selectSession(use.session_id)}>{use.title}</button> : use.title} · {use.version} · {phases[use.phase]}
    <small className="mono">{use.snapshot_id}</small>
  </p>) : <p className="hint">暂无已记录的会话引用；未记录的原生使用不在此列表中。</p>}</div>;
}

function CapabilityEditor({ action, item, read, connected, close, saved }: {
  action: string; item?: Item; read: Read; connected: boolean; close: () => void; saved: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [preview, setPreview] = useState<Preview>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const title = ({ register: '登记或导入能力', update: '更新登记版本', disable: '停用新引用', enable: '恢复新引用', remove: '移除登记' })[action];
  const editsDefinition = action === 'register' || action === 'update';
  useEffect(() => { dialog.current?.showModal(); }, []);
  async function inspect(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true); setError('');
    const data = new FormData(event.currentTarget);
    const value = (name: string) => String(data.get(name) ?? '').trim();
    const id = item?.asset.id ?? value('id');
    const change: Change = { action, asset_id: id, base_revision: item?.revision ?? '' };
    if (editsDefinition) change.asset = { id, name: value('name'), kind: item?.asset.kind ?? value('kind'), version: value('version'), source: value('source'),
      management: value('management'), backends: data.getAll('backend').map(String), ...(value('codex_mcp') ? { codex_mcp: value('codex_mcp') } : {}), ...(value('codex_skill') ? { codex_skill: value('codex_skill') } : {}) };
    try { setPreview(await read<Preview>('/api/capabilities/preview', change)); }
    catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  }
  async function apply() {
    if (!preview) return;
    setBusy(true); setError('');
    const fields = { ...preview.request, preview_hash: preview.preview_hash };
    const payload = JSON.stringify(fields), key = 'netcap.capability-command.v1';
    try {
      const previous = JSON.parse(sessionStorage.getItem(key) ?? 'null') as { id: string; payload: string } | null;
      const id = previous?.payload === payload ? previous.id : crypto.randomUUID();
      sessionStorage.setItem(key, JSON.stringify({ id, payload }));
      await read('/api/commands', { command_id: id, kind: 'manage_capability', capability_change: fields });
      sessionStorage.removeItem(key); saved();
    } catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  }
  const importedFrom = item?.versions.at(-1)?.imported_from;
  const source = importedFrom && !importedFrom.startsWith('package:') ? importedFrom : item?.asset.source;
  return <dialog className="dialog capability-dialog" ref={dialog} onClose={close} aria-labelledby="capability-edit-title">
    <div className="section-heading"><h2 id="capability-edit-title">{title}</h2><button onClick={close} aria-label="关闭能力管理">×</button></div>
    <p className="muted">先预览来源与已记录引用，再保存登记。原生执行和已有快照不会随登记变更。</p>
    <form onSubmit={event => void inspect(event)} onChange={() => { setPreview(undefined); setError(''); }}>
      {editsDefinition ? <fieldset disabled={busy}>
        <label>资产 ID<input name="id" defaultValue={item?.asset.id} readOnly={!!item} required pattern="(?:[A-Za-z0-9_]|-)+" maxLength={100} /></label>
        <label>显示名称<input name="name" defaultValue={item?.asset.name} required maxLength={200} /></label>
        <label>能力类型<select name="kind" defaultValue={item?.asset.kind ?? 'skill'} disabled={!!item}><option value="skill">Skill</option><option value="mcp">MCP 定义</option><option value="cli_plugin">CLI 插件</option></select></label>
        <label>新版本标识<input name="version" required placeholder="例如：v1" maxLength={200} /></label>
        <label>资源来源<input name="source" required defaultValue={source} placeholder="资源文件或目录的绝对路径" /></label>
        <label>管理方式<select name="management" defaultValue={item?.management ?? 'external'}><option value="external">外部管理 · 保留原目录</option><option value="managed">平台管理 · 复制为固定版本</option></select></label>
        <fieldset className="backend-options"><legend>声明适用的 CLI</legend>{['codex', 'claude'].map(backend => <label key={backend}>
          <input type="checkbox" name="backend" value={backend} defaultChecked={item ? item.asset.backends.includes(backend) : true} />{backend}</label>)}</fieldset>
        <label>Codex 原生 MCP 服务名（可选）<input name="codex_mcp" defaultValue={item?.asset.codex_mcp} placeholder="仅用于当前 profile 中的专属 MCP" /></label>
        <label>Codex 原生 Skill 入口（可选）<input name="codex_skill" defaultValue={item?.asset.codex_skill} placeholder="原生来源发现中的绝对 SKILL.md 路径" /></label>
        <p className="hint">原生 Skill 映射仅适用于 Codex 外部资源，需指向登记的同一入口。留空时沿用独立 Skill 资源交付；受管和插件来源需按各自规则选择。</p>
      </fieldset> : <><p>{item?.asset.name} · {item?.asset.version}</p><p className="hint">{action === 'remove' ? '从可选能力中移除登记；版本、资源文件和会话历史仍保留。' : '只改变新执行是否可以引用；已有原生能力不会因此卸载。'}</p></>}
      <button disabled={!connected || busy}>{busy ? '正在处理…' : '预览影响'}</button>
    </form>
    {preview ? <section className="capability-preview" aria-label="能力变更预览"><h3>本次影响</h3>
      <p>{preview.boundary}</p>
      {preview.candidate ? <><p>{preview.candidate.asset.name} · {preview.candidate.asset.version} · {preview.candidate.management === 'managed' ? '固定版本副本' : '外部来源'}</p>
        <p className="mono">{preview.candidate.asset.source}</p><p>{states[preview.candidate.source_state]}</p>
        {preview.candidate.source_error ? <p className="error">{preview.candidate.source_error}</p> : null}<CapabilityDiagnosis value={preview.candidate.diagnosis} /></> : null}
      <Uses uses={preview.uses} />
      <button className="primary" disabled={!connected || busy} onClick={() => void apply()}>确认保存登记</button>
    </section> : null}
    {error ? <p className="error" role="alert">{error}</p> : null}
  </dialog>;
}
