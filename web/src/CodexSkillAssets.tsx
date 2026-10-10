import type { PluginEvidence, PluginSelection } from './ClaudePluginSelection';
import type { CapabilityAsset } from './ExecutionDetails';

export function CodexSkillAssets({ selections, evidence, assets, connected }: {
  selections: PluginSelection[]; evidence?: PluginEvidence[]; assets: CapabilityAsset[]; connected: boolean;
}) {
  return <section aria-label="本次原生 Skill 选择">
    <p className="snapshot-subtitle">本次原生 Skill 选择</p>
    {selections.map(selection => {
      const asset = assets.find(asset => asset.id === selection.asset_id);
      const observed = evidence?.find(item => item.asset_id === selection.asset_id);
      const component = selection.components[0];
      return <div className="snapshot-asset" key={selection.asset_id}>
        <p>{asset?.name ?? component?.name ?? selection.asset_id} · {selection.mode === 'enabled' ? '启用' : '禁用'}</p>
        <small>Skill 资产 · {asset?.version ?? '版本未报告'} · {selection.skill_asset?.kind === 'file' ? '单文件入口' : '目录及相对资料'}</small>
        <p>{!connected ? '已断开，原生状态待重新核对' : observed?.verified ? '固定资源副本与整包策略已核对' : '资源交付与原生策略待核对'}</p>
        {observed ? <small>{observed.detail}</small> : null}
        <details><summary>资源与交付来源</summary>
          <p className="mono">原资源：{selection.source_path}</p>
          <p className="mono">交付入口：{selection.cache_path}/{component?.path}</p>
          <p className="mono">原生名称：{component?.native_name}</p>
          <p className="mono">资源摘要：{selection.source_hash}</p>
        </details>
      </div>;
    })}
    <p className="hint">每个会话使用原资源的独立版本副本，保留正文与相对资料。上方逐轮上下文可核对正文载入；交付准备不代表方法已使用或 Gate 通过。禁用不抹除历史，也不撤销文件权限。</p>
  </section>;
}
