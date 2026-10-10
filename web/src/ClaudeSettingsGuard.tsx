export type SettingsGuard = {
  version: number; state: string; detail: string;
  changes: { source: string; path: string; at: string; decision: string }[];
};

export function ClaudeSettingsGuard({ version, evidence, connected }: {version?: number; evidence?: SettingsGuard; connected: boolean}) {
  if (!version) return <p className="hint">此执行未登记本地设置自动应用限制；来源版本记录仍独立核对。</p>;
  const current = connected && evidence?.version === version;
  return <section aria-label="本地设置自动应用限制">
    <p>本地设置自动应用限制 · {current ? evidence.state === 'registered' ? '已注册' : '当前不可用' : '当前未核对'}</p>
    <p className="hint">{current ? evidence.detail : '连接恢复并取得当前证据后可核对状态。旧记录不能证明当前仍能阻止自动应用。'}</p>
    {evidence?.version === version && evidence.changes?.length ? <><p>最近的阻止决策记录</p>{evidence.changes.map((change, index) => <div className="snapshot-asset" key={`${change.at}/${index}`}>
      <p>已请求阻止 · {change.source === 'project_settings' ? '项目设置' : change.source === 'local_settings' ? '本地设置' : '来源待核对'}</p>
      <p className="mono">{change.path || '未取得来源路径'}</p>
      <small>{new Date(change.at).toLocaleString()}</small>
    </div>)}</> : null}
    <p className="hint">仅涉及普通项目/本地设置。受管策略、脚本资源和其他来源未被固定；来源文件变化仍会阻止后续输入。这里不自动采纳新配置。</p>
  </section>;
}
