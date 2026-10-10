export type DirectorySample = {
  source: string; at: string; complete: boolean; snapshot_id: string;
  entries: { name: string; description: string; native_key: string; path: string; resolved_path: string; source: string; control: string; detail: string }[];
  errors: { path?: string; detail: string }[];
  saved_selections: { asset_id: string; version: string; name: string; path: string; mode: string; discovered: boolean }[];
};

export function ClaudeSkillDirectories({ sample }: { sample: DirectorySample }) {
  return <section aria-label="Claude 用户与项目 Skill 目录">
    <details><summary>用户与项目目录入口 · {sample.entries.length} 项</summary>
      <p className="hint">只扫描保存配置的 skills 目录与项目祖先的 .claude/skills。此处列出磁盘入口，不代表原生启用、已加载或已使用；受管、插件、旧 commands 目录及其他动态来源不在扫描范围。</p>
      <p>{sample.complete ? '支持范围内的目录采样完成' : '目录采样不完整，部分来源待核对'}</p>
      <small>目录采样时间：{new Date(sample.at).toLocaleString()}</small>
      {sample.entries.map(entry => <div className="snapshot-asset" key={`${entry.source}/${entry.path}`}>
        <p>{entry.name} · 磁盘入口</p>
        {entry.description ? <p>{entry.description}</p> : null}
        <small>目录来源：{entry.source === 'userSettings' ? '用户' : '项目'}</small>
        <p className="mono">原生目录键：{entry.native_key}</p>
        <p className="mono">登记入口：{entry.path}</p>
        {entry.resolved_path !== entry.path ? <p className="mono">解析路径：{entry.resolved_path}</p> : null}
        <p>{entry.control === 'ambiguous' ? '来源有歧义' : entry.control === 'requires_binding' ? '需登记并核对原生准入' : '控制范围待核对'}</p>
        <small>{entry.detail}</small>
      </div>)}
      {!sample.entries.length ? <p>未找到可展示的目录入口；不代表没有其他原生来源。</p> : null}
      {sample.errors.map((error, index) => <div className="error" key={`${error.path}/${index}`}><p>{error.detail}</p>{error.path ? <p className="mono">{error.path}</p> : null}</div>)}
      {sample.saved_selections.length ? <section aria-label="Claude 已保存的目录选择">
        <h4>本会话已保存的目录选择</h4>
        <p className="mono">快照：{sample.snapshot_id}</p>
        {sample.saved_selections.map(saved => <div className="snapshot-asset" key={saved.asset_id}>
          <p>{saved.name} · 已保存{saved.mode === 'enabled' ? '启用' : '禁用'}</p>
          <small>资产 {saved.asset_id} · 版本 {saved.version}</small><p className="mono">{saved.path}</p>
          <p className="hint">{saved.discovered ? '本次目录采样包含此入口；当前生效状态仍需原生核对。' : '本次目录采样未返回此入口；原快照与选择仍保留。'}</p>
        </div>)}
      </section> : null}
      <p className="hint">目录采样与上方原生清单分别记录，不按同名自动关联。名称、目录键、路径与完整资源需在登记和改配时继续核对；本次读取不改变配置、版本或输入权限。</p>
    </details>
  </section>;
}
