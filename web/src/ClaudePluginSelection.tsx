export type PluginComponent = { kind: string; name: string; path: string; native_name: string; count: number };
export type PluginSelection = { asset_id: string; name: string; mode: string; version: string; source_path: string; source_hash: string; components: PluginComponent[]; native_source?: string; cache_path?: string; hooks?: { trust: string; inspection: { definitions: { key: string; path: string; event: string; definition_hash: string }[] } }; skill_asset?: { kind: string; root_mode: number }; manifest_source?: { identity: string; settings: string; layout: string } };
export type PluginEvidence = { asset_id: string; verified: boolean; at: string; detail: string; native_source: string; coverage?: string };

export function ClaudePluginSelection({ selections, evidence, connected, backend = 'claude' }: { selections: PluginSelection[]; evidence?: PluginEvidence[]; connected: boolean; backend?: string }) {
  return <section aria-label="本次 CLI 插件选择">
    <p className="snapshot-subtitle">本次 CLI 插件选择</p>
    {selections.map(selection => {
      const observed = evidence?.find(item => selection.asset_id ? item.asset_id === selection.asset_id : item.native_source === selection.native_source);
      return <div className="snapshot-asset" key={selection.asset_id || selection.native_source}>
        <p>{selection.name} · {selection.mode === 'enabled' ? '启用整包' : '禁用整包'}</p>
        <small>插件声明版本：{selection.version || '未声明'}</small>
        {selection.hooks ? <p>本版本 hook：{selection.hooks.trust === 'trusted' ? '已明确信任' : '未信任'} · {selection.hooks.inspection.definitions.length} 项定义；实际执行结果另行记录。</p> : null}
        {selection.manifest_source ? <div className="plugin-manifest-source">
          <p>身份文件：{selection.manifest_source.identity}</p>
          <p>Codex 配置来源：{selection.manifest_source.settings}</p>
          <small>根 manifest 决定插件名称与版本；内联配置存在时整体替代旧 overlay。</small>
        </div> : null}
        <p className="mono">来源键：{selection.native_source ?? `${selection.name}@inline`}</p>
        <p className="mono">{selection.source_path}</p>
        {selection.cache_path ? <><small>{selection.mode === 'enabled' ? '本次版本的加载副本' : '本次禁用 · 版本绑定路径'}</small><p className="mono">{selection.cache_path}</p></> : null}
        <p>{!connected ? '已断开，原生状态待重新核对' : observed?.verified ? observed.coverage === 'package_and_connection_hook_configuration' ? '固定包、整包策略与本连接 hook 配置已核对' : observed.coverage === 'cached_package_and_thread_policy' ? selection.components.some(component => component.kind === 'mcp') ? '固定插件包、MCP 与整包策略已核对' : '固定插件包与整包策略已核对' : '原生包与组件清单匹配' : '原生包与组件待核对'}</p>
        {observed ? <><small>{observed.detail}</small><p>最近核对：{observed.at ? new Date(observed.at).toLocaleString() : '尚未采样'}</p></> : null}
        <details><summary>包内组件 · {selection.components.length} 项</summary>
          {selection.components.map(component => <p key={`${component.kind}/${component.native_name}`}>
            {component.kind} · {component.native_name}{component.count > 1 ? ` · ${component.count} 项` : ''}<small className="mono">{component.path}</small>
          </p>)}
        </details>
      </div>;
    })}
    <p className="hint">选择绑定完整目录、来源键与版本。原 ID 重开时重新核对；来源变化、来源冲突或组件不匹配会阻止输入。{backend === 'codex' ? '混合插件的核对覆盖固定包、MCP 和整包策略；Skill 描述及正文载入请查看逐轮上下文，历史内容不会因禁用而删除。' : '清单匹配仅证明本次加载情况，hook 执行、工具调用和方法使用仍需各自的结果。'}</p>
    {backend === 'claude' ? <p className="hint">可在下方“原会话能力改配”新增、启停、明确替换版本或恢复继承；保存候选不会立即生效。未选择的其他能力保留原绑定。</p> : <p className="hint">可在下方“原会话能力改配”新增、启停、明确替换版本或恢复继承；本地 MCP 插件、标准 Skill 插件及两者混合包可选，各会话保留独立版本副本。Skill 可使用插件声明的自定义目录，包内组件显示实际入口。含同步 hook 的旧格式插件可在审核完整版本后选择信任；异步与通用 hook 仍待接通。保存候选不会立即生效。</p>}
  </section>;
}
