import { useState } from 'react';

type FileSource = { path: string; resolved_path: string; kind: string; sha256?: string; mode?: number };
type Preview = {
  source_version: string; startup_source_version?: string; snapshot_id?: string; sources_stable: boolean; complete: boolean; at: string;
  setting: { source_key: string; enabled: boolean };
  artifact?: { original: FileSource; file: FileSource };
  validation?: { schema_valid: boolean; cli_version: string; process_stopped: boolean;
    preservation?: { matches: boolean; differences: string[]; baseline_schema_valid: boolean; baseline_process_stopped: boolean };
    plugin_settings: { source_key: string; requested_enabled: boolean; actual_enabled: boolean | null; matches: boolean }[];
    errors: { file: string; field: string }[] };
  cleanup: { state: string; recovered: number; issues: string[] };
  problems: string[];
};
const cleanupStates: Record<string, string> = {
  released: '临时设置文件已回收', retained: '临时设置文件尚未回收',
  not_created: '未生成临时设置文件', not_acquired: '创建未完成，已核查可回收记录',
};
const preservationDifferences: Record<string, string> = {
  effective_settings: '其他有效设置（含未选择的插件）', source_settings: '其他来源设置',
  applied_settings: '已采用参数', permission_rules: '权限规则与工作目录', schema: '设置格式',
};

export function ClaudePluginSettingsPreview({ sessionId, baseSnapshotId, sourceKey, sourceVersion, available, canSave, read }: {
  sessionId: string; sourceKey: string; sourceVersion: string; available: boolean;
  baseSnapshotId: string; canSave: boolean;
  read: <T>(path: string, body?: unknown) => Promise<T>;
}) {
  const [report, setReport] = useState<Preview>();
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState('');
  const [error, setError] = useState('');
  async function preview(mode: 'enabled' | 'disabled') {
    setReport(undefined); setError(''); setSaved(''); setLoading(true);
    try {
      const result = await read<Preview>(`/api/sessions/${encodeURIComponent(sessionId)}/plugins/settings-preview`, {
        source_key: sourceKey, source_version: sourceVersion, mode,
      });
      if (result.source_version !== sourceVersion || result.setting.source_key !== sourceKey || result.setting.enabled !== (mode === 'enabled')) {
        throw new Error('预览结果与本次来源选择不一致，请重新读取安装登记。');
      }
      setReport(result);
    } catch (e) { setError((e as Error).message); }
    finally { setLoading(false); }
  }
  const observation = report?.validation?.plugin_settings.find(item => item.source_key === sourceKey);
  const preservation = report?.validation?.preservation;
  const stable = report?.sources_stable && report.problems.length === 0 && preservation?.matches === true;
  const savable = stable && report?.snapshot_id === baseSnapshotId && !!report.startup_source_version &&
    report.validation?.schema_valid && report.validation.process_stopped && observation?.matches && report.validation.plugin_settings.every(item => item.matches) &&
    report.cleanup.state === 'released' && report.cleanup.issues.length === 0;
  async function save() {
    if (!report || !savable || !canSave || saving) return;
    setSaving(true); setError('');
    const mode = report.setting.enabled ? 'enabled' : 'disabled';
    const key = `netcap.plugin-settings-stage.v1/${sessionId}/${baseSnapshotId}/${sourceKey}/${sourceVersion}/${mode}`;
    try {
      // Keep the request identity across a lost reply or page reload. A new
      // click must not create a second proposal while the first is uncertain.
      const commandId = sessionStorage.getItem(key) || crypto.randomUUID();
      sessionStorage.setItem(key, commandId);
      const receipt = await read<{ command_id: string; session_id: string; status: string; pending_change_id: string }>(
        `/api/sessions/${encodeURIComponent(sessionId)}/plugins/settings-stage`, {
          command_id: commandId, base_snapshot_id: baseSnapshotId, source_key: sourceKey, source_version: sourceVersion, mode,
        });
      if (receipt.command_id !== commandId || receipt.session_id !== sessionId || receipt.status !== 'pending' || !receipt.pending_change_id) {
        throw new Error('保存回执不完整，请用同一请求重试核对。');
      }
      sessionStorage.removeItem(key);
      setSaved(receipt.pending_change_id);
    } catch (e) { setError((e as Error).message); }
    finally { setSaving(false); }
  }
  return <div className="startup-settings">
    <p className="hint">核对该来源的启用或排除设置。预览使用临时设置副本，本会话的配置保持不变。</p>
    <button disabled={!available || loading || saving} aria-label={`${sourceKey}：核对启用设置`} onClick={() => void preview('enabled')}>核对启用设置</button>{' '}
    <button disabled={!available || loading || saving} aria-label={`${sourceKey}：核对排除设置`} onClick={() => void preview('disabled')}>核对排除设置</button>
    {loading ? <p role="status">正在核对设置和来源版本…</p> : null}
    {error ? <p className="error" role="alert">{error}</p> : null}
    {report ? <>
      <p role="status">{!report.validation ? '预览未完成，请查看诊断。' : !report.validation.schema_valid ? '原生设置格式无效。' : !stable ? '预览未完成，请查看诊断。' : observation?.matches ? '目标设置核对相符；本会话配置未改变。' : '原生有效值与目标设置不同。'}</p>
      <p>目标：{report.setting.enabled ? '启用' : '排除'} {report.setting.source_key}</p>
      {report.validation ? <p>原生观察值：{observation?.actual_enabled === true ? '启用' : observation?.actual_enabled === false ? '排除' : '没有可确认的布尔值'} · Claude {report.validation.cli_version}</p> : null}
      {report.validation ? <div className="settings-preservation">
        <p>{preservation?.matches ? '原文件与临时文件的其他有效设置、来源、已采用参数和权限规则：本次观察一致。' : preservation ? '原文件与临时文件未通过设置保留核对。' : '未取得原文件与临时文件的保留核对依据。'}</p>
        {preservation?.differences.map(kind => <p className="error" key={kind}>保留核对发现变化：{preservationDifferences[kind] ?? '未知类别'}</p>)}
      </div> : null}
      <p>{report.sources_stable ? '本次读取的设置和插件声明来源未变化。' : '来源稳定性未通过核对。'}</p>
      {report.artifact ? <details><summary>本次设置文件</summary>
        <p className="mono">原设置：{report.artifact.original.kind === 'absent' ? '未指定自定义设置文件' : report.artifact.original.path}</p>
        {report.artifact.original.kind !== 'absent' && report.artifact.original.path !== report.artifact.original.resolved_path ? <p className="mono">原设置解析路径：{report.artifact.original.resolved_path}</p> : null}
        <p className="mono">临时设置：{report.artifact.file.path}</p>
        <small className="mono">文件内容标识 {report.artifact.file.sha256} · 权限 0600</small>
      </details> : null}
      <p>{cleanupStates[report.cleanup.state] ?? '临时文件回收状态待核对'}{report.cleanup.recovered > 0 ? `；另处理 ${report.cleanup.recovered} 份遗留记录` : ''}。</p>
      {report.cleanup.issues.map((issue, i) => <p className="error" key={`cleanup-${i}`}>{issue}</p>)}
      {report.problems.map((issue, i) => <p className="error" key={`problem-${i}`}>{issue}</p>)}
      {report.validation?.errors.map((issue, i) => <p className="error" key={`schema-${i}`}>设置格式错误：{issue.field}<small className="mono">{issue.file}</small></p>)}
      <small>核对时间：{new Date(report.at).toLocaleString()}</small>
      <p className="hint">保存时会重新核对插件目录、安装登记和启动设置版本，并保留本会话的其他插件选择。保存后仍使用原配置；准备和应用在下方单独操作。</p>
      <button disabled={!available || !canSave || !savable || saving || !!saved} aria-label={`${sourceKey}：保存待应用方案`} onClick={() => void save()}>{saving ? '正在重新核对并保存…' : '保存待应用方案'}</button>
      {saved ? <p role="status">待应用方案已保存，尚未生效。可在下方查看依据、准备应用或撤销。</p> : null}
      {!canSave ? <p className="hint">当前已有待应用方案或绑定正在更新；请先核对、撤销原方案后再保存。</p> : null}
      <p className="hint">比较来自独立校验进程。实际应用仍要核对来源和加载结果；此比较不证明完整权限、远程策略新鲜度或后续策略保持不变。</p>
    </> : null}
  </div>;
}
