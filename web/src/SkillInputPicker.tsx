import type { ExecutionSnapshot } from './ExecutionDetails';

export type SkillInput = { asset_id: string; name: string };
export type SkillInputChoice = { snapshotId: string; items: SkillInput[] };

export function SkillInputPicker({ snapshot, selected, disabled, onChange }: {
  snapshot?: ExecutionSnapshot; selected: SkillInput[]; disabled: boolean; onChange: (choice: SkillInputChoice) => void;
}) {
  if (snapshot?.instance.kind !== 'codex') return null;
  const options = (snapshot.plugin_selections ?? []).filter(pack => pack.mode === 'enabled').flatMap(pack =>
    pack.components.filter(component => component.kind === 'skill').map(component => ({
      asset_id: pack.asset_id, name: component.native_name,
      label: pack.skill_asset ? `${component.name} · ${snapshot.capability_refs.find(ref => ref.asset.id === pack.asset_id)?.asset.name ?? pack.asset_id}` : component.native_name,
      version: snapshot.capability_refs.find(ref => ref.asset.id === pack.asset_id)?.asset.version,
    })));
  options.push(...(snapshot.skill_selections ?? []).filter(skill => skill.mode === 'enabled' && skill.delivery === 'native_directory').map(skill => ({
    asset_id: skill.asset_id, name: skill.name, label: `${skill.name} · 原生目录`,
    version: snapshot.capability_refs.find(ref => ref.asset.id === skill.asset_id)?.asset.version,
  })));
  if (!options.length) return null;
  return <details className="skill-input-picker"><summary>本条消息附带 Skill{selected.length ? ` · 已选 ${selected.length} 项` : ''}</summary>
    <p className="hint">选择本次已启用的 Skill，随消息请求载入正文，每条最多 16 项。只影响这条消息；改配或切换会话后请重新选择。</p>
    <fieldset disabled={disabled}>
      <legend className="sr">本条消息的 Skill</legend>
      {options.map(option => {
        const checked = selected.some(item => item.asset_id === option.asset_id && item.name === option.name);
        return <label key={`${option.asset_id}/${option.name}`}><input type="checkbox" checked={checked} disabled={!checked && selected.length >= 16} onChange={event => {
          const remaining = selected.filter(item => item.asset_id !== option.asset_id || item.name !== option.name);
          onChange({ snapshotId: snapshot.id, items: event.target.checked ? [...remaining, { asset_id: option.asset_id, name: option.name }] : remaining });
        }} />{option.label}<small>资产版本：{option.version ?? '未报告'}</small></label>;
      })}
    </fieldset>
  </details>;
}
