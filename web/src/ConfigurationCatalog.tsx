import { useState } from 'react';

type Layer = { id: string; kind: string; version: string; disabled: boolean;
  file?: { path: string; resolved_path: string; kind: string; sha256?: string; mode?: number } };
type Effort = { state: string; value?: string };
type Settings = { version: number; reasoning_effort: Effort };
type SettingsDifference = { base_snapshot_id: string; status: string; before?: Effort; after?: Effort;
  effort_changed: boolean; effort_sources_changed: boolean; other_values_changed: boolean; other_sources_changed: boolean };
type Catalog = {
  native_id: string; cwd: string; at: string; scope: string; complete: boolean;
  configuration_sha256: string; origins_sha256: string; layers_sha256: string;
  requirements_sha256: string; requirements_present: boolean; layers: Layer[];
  origins: { field: string; layer_id: string }[]; other_origin_count: number;
  settings?: Settings; settings_difference?: SettingsDifference;
};
export type ConfigurationSources = Pick<Catalog, 'cwd' | 'configuration_sha256' | 'origins_sha256' | 'layers_sha256' | 'layers' | 'origins' | 'other_origin_count' | 'settings'> & { version: number; profile: string };
const kinds: Record<string, string> = { packagedDefaults: '随包默认', system: '系统', user: '用户', project: '项目',
  mdm: '设备管理', enterpriseManaged: '企业受管', sessionFlags: '进程参数',
  legacyManagedConfigTomlFromFile: '旧受管文件', legacyManagedConfigTomlFromMdm: '旧设备管理' };

function effortLabel(effort?: Effort) {
  if (effort?.state === 'unset') return '未显式设置（未推断模型默认值）';
  if (effort?.state === 'set' && ['none', 'minimal', 'low', 'medium', 'high', 'xhigh'].includes(effort.value ?? '')) return effort.value;
  return '值尚未识别，正文未展示';
}

function SettingsComparison({ difference }: { difference?: SettingsDifference }) {
  const known = difference?.status === 'changed' || difference?.status === 'unchanged';
  return <section aria-label="与执行快照设置比较">
    <p className="snapshot-subtitle">磁盘设置差异</p>
    {!known ? <p className="hint">{!difference || difference.status === 'unrecorded' ? '原快照或本次读取未记录设置比较依据，不能补造原值。' : '设置比较依据的版本或格式尚未核对。'}</p> : <>
      <p>reasoning effort：{effortLabel(difference.before)} → {effortLabel(difference.after)} · {difference.effort_changed ? '已变化' : '未变化'}</p>
      <p>effort 来源（含被覆盖层）：{difference.effort_sources_changed ? '已变化' : '未变化'}</p>
      <p>其他合并设置：{difference.other_values_changed ? '已变化' : '未变化'}</p>
      <p>其他设置来源及层结构：{difference.other_sources_changed ? '已变化' : '未变化'}</p>
      <p className="hint">其他设置只比较摘要；被覆盖的配置、自定义键和来源层变化也会计入。文件字节与权限另按来源版本核对。</p>
    </>}
    <p className="hint">这是保存版本与当前磁盘的对照，不代表线程已采用新设置；符合条件的非空 effort 或文件版本变化可在“原会话配置改配”中明确预览、保存和应用；其他设置变更仍未接通。</p>
  </section>;
}

export function ConfigurationCatalog({ sessionId, connected, read, saved }: {
  sessionId: string; connected: boolean; read: <T>(path: string, body?: unknown) => Promise<T>; saved?: ConfigurationSources;
}) {
  const [catalog, setCatalog] = useState<Catalog>();
  const [previous, setPrevious] = useState<Catalog>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  async function sample() {
    const prior = catalog;
    setCatalog(undefined); setPrevious(undefined); setError(''); setLoading(true);
    try {
      const result = await read<Catalog>(`/api/sessions/${encodeURIComponent(sessionId)}/configuration/catalog`, {});
      if (result.scope !== 'disk_configuration' || result.complete) throw new Error('配置来源覆盖范围尚未核对。');
      setCatalog(result); setPrevious(prior);
    } catch (error) { setError((error as Error).message); }
    finally { setLoading(false); }
  }
  return <details className="skill-discovery configuration-catalog"><summary>Codex 当前磁盘配置来源</summary>
    <p className="hint">按保存的执行配置与工作目录读取原生合并来源；已加载线程可能仍使用旧配置。读取不会应用配置、更新快照或授予输入权。</p>
    <button disabled={!connected || loading} onClick={() => void sample()}>{loading ? '正在读取配置来源…' : '读取当前配置来源'}</button>
    {error ? <p role="alert" className="error">{error}</p> : null}
    {catalog ? <>
      <p role="status">当前磁盘配置来源 · {catalog.layers.length} 层 · 线程全部有效配置未核对</p>
      <small>读取时间：{new Date(catalog.at).toLocaleString()}</small><p className="mono">工作目录：{catalog.cwd}</p>
      <section aria-label="与执行快照来源比较">
        <p>{!saved ? '此执行未记录磁盘合并来源版本，不能补造历史对照。' : saved.version !== 1 ? '保存的配置来源版本格式尚未核对。' : saved.cwd === catalog.cwd && saved.configuration_sha256 === catalog.configuration_sha256 && saved.origins_sha256 === catalog.origins_sha256 && saved.layers_sha256 === catalog.layers_sha256 && JSON.stringify(saved.layers) === JSON.stringify(catalog.layers) ? '本次读取与执行快照记录的配置来源一致。' : '本次配置来源与执行快照不同；原版本保持不变。'}</p>
        <p className="hint">来源一致不代表线程全部配置、受管权限或输入权已核对。</p>
      </section>
      <SettingsComparison difference={catalog.settings_difference} />
      {previous ? <section aria-label="与上次配置来源读取比较">
        <p>与本页上次读取比较；不是执行快照版本差异。</p>
        <p>磁盘合并值：{previous.configuration_sha256 === catalog.configuration_sha256 ? '未变化' : '已变化'}；字段来源：{previous.origins_sha256 === catalog.origins_sha256 ? '未变化' : '已变化'}</p>
        <p>来源层及文件：{previous.layers_sha256 === catalog.layers_sha256 && JSON.stringify(previous.layers) === JSON.stringify(catalog.layers) ? '未变化' : '已变化'}；受管要求：{previous.requirements_sha256 === catalog.requirements_sha256 ? '未变化' : '已变化'}</p>
      </section> : null}
      <p className="hint">以下保留原生返回顺序；具体字段采用哪层以字段来源为准。原生层版本不包含文件排版和权限，文件摘要另列。</p>
      {catalog.layers.map((layer, index) => <div className="snapshot-asset" key={layer.id}>
        <p>来源 {index + 1} · {kinds[layer.kind] ?? layer.kind} · {layer.disabled ? '原生未启用' : '原生启用'}</p>
        {layer.file ? <><p className="mono">{layer.file.path}</p>
          {layer.file.path !== layer.file.resolved_path ? <p className="mono">实际路径：{layer.file.resolved_path}</p> : null}
          <small>{layer.file.kind === 'absent' ? '读取时文件不存在' : `文件权限：${((layer.file.mode ?? 0) & 0o777).toString(8).padStart(3, '0')}`}</small>
        </> : <small>非文件来源；身份以摘要区分，未复制受管内容。</small>}
        <details><summary>来源版本与文件摘要</summary><p className="mono">原生层版本：{layer.version}</p>{layer.file?.sha256 ? <p className="mono">文件 SHA256：{layer.file.sha256}</p> : null}<p className="mono">来源身份：{layer.id}</p></details>
      </div>)}
      <section aria-label="磁盘配置字段来源"><p className="snapshot-subtitle">磁盘配置字段来源</p>
        {catalog.origins.map(origin => <p key={origin.field}>{origin.field} · 来源 {catalog.layers.findIndex(layer => layer.id === origin.layer_id) + 1}</p>)}
        {!catalog.origins.length ? <p>本次未报告可展示的字段来源。</p> : null}
        <p className="hint">仅列固定公共字段；另有 {catalog.other_origin_count} 项来源计入摘要，省略自定义键与配置正文。线程覆盖、插件实际加载与动态账号权限仍需另行核对。</p>
      </section>
      <p>原生受管要求：{catalog.requirements_present ? '报告存在，正文未展示' : '明确报告无要求'}；此结果不代表完整权限已验证。</p>
      <details><summary>本次合并配置与受管要求摘要</summary><p className="mono">磁盘合并配置：{catalog.configuration_sha256}</p><p className="mono">受管要求：{catalog.requirements_sha256}</p></details>
    </> : null}
  </details>;
}

export function ConfigurationSourceSnapshot({ sources }: { sources?: ConfigurationSources }) {
  return <details className="configuration-source-snapshot"><summary>Codex 配置来源版本</summary>
    {!sources ? <p className="hint">此快照未记录磁盘合并来源版本；不会补造历史依据。</p> : sources.version !== 1 ? <p className="error">配置来源版本格式尚未核对。</p> : <>
      <p>已保存 {sources.layers.length} 个原生配置层及文件版本；修改源文件不会自动升级执行。</p>
      {sources.settings?.version === 1 ? <p>记录时的磁盘 reasoning effort：{effortLabel(sources.settings.reasoning_effort)}；其他设置及来源保留摘要供版本比较。</p> : <p className="hint">此版本未记录可用的设置比较依据，不能从当前文件补造原值。</p>}
      {sources.layers.map(layer => <div key={layer.id} className="snapshot-asset"><p>{kinds[layer.kind] ?? layer.kind} · {layer.disabled ? '记录时未启用' : '记录时启用'}</p>{layer.file ? <p className="mono">{layer.file.path} · {layer.file.kind === 'absent' ? '记录时不存在' : `文件权限 ${((layer.file.mode ?? 0) & 0o777).toString(8).padStart(3, '0')}`}</p> : <p>非文件配置层</p>}<small className="mono">原生层版本：{layer.version}</small></div>)}
      <p className="hint">本地检查核对已记录文件；连接原生后还会核对合并值、来源集合和字段来源。漂移会阻止继续输入与恢复，原文件还原后可重新核对。</p>
      <p className="hint">受管权限要求沿原生权限流程处理；外部脚本、动态账号能力与线程全部有效配置仍未完整覆盖。</p>
    </>}
  </details>;
}
