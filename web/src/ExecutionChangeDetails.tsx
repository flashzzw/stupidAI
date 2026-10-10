import { useEffect, useState } from 'react';
import { PluginHookReview, type HookReview } from './PluginHookReview';
import type { CapabilityAsset, ExecutionSnapshot } from './ExecutionDetails';
import type { CommandFields } from './PendingAction';
import type { SettingsGuard } from './ClaudeSettingsGuard';
import { ConfigurationFailureDetails, type ConfigurationFailure } from './ConfigurationFailureDetails';
import { ExecutionRecoveryDetails } from './ExecutionRecoveryDetails';

type Selection = { asset_id: string; mode: string; version?: string; hook_review?: HookReview };
type Change = { asset_id: string; kind?: string; name?: string; source_key?: string; server: string; before: string; after: string; before_version?: string; after_version?: string; before_hook_trust?: string; after_hook_trust?: string };
type FileVersion = {path:string;resolved_path:string;kind:string;sha256?:string;mode?:number};
type HookSourceChange = {source:string;before?:FileVersion;after?:FileVersion;detail:string;before_origin?:string;after_origin?:string};
type LocalChange = {before:FileVersion;after:FileVersion};
type ConfigurationChange = { settings: { before?: {value?:string}; after?:{value?:string}; effort_changed:boolean; effort_sources_changed:boolean }; files: {kind:string;before:FileVersion;after:FileVersion}[] };
type Preview = { base_snapshot_id: string; pending_change_id: string; preview_hash: string; next_revision: number; changes: Change[]; boundary: string; local_settings_changes?: LocalChange[]; codex_hook_source_changes?: HookSourceChange[]; codex_configuration_change?:ConfigurationChange };
export type ExecutionChange = { installed_plugin_change?: boolean; id: string; base_snapshot_id?: string; status: string; snapshot: ExecutionSnapshot; recovery_snapshot?: ExecutionSnapshot; changes: Change[]; boundary: string; baseline?: string; operation_id?: string; recovery_operation_id?: string; failure?:ConfigurationFailure; failure_detail?:string; adopt_local_settings?: boolean; local_settings_changes?: LocalChange[]; adopt_codex_hook_sources?:boolean; adopt_codex_configuration_sources?:boolean; codex_configuration_change?:ConfigurationChange; hook_source_proof?:{version:number;at:string}; codex_hook_source_changes?:HookSourceChange[] };

function ConfigurationDiff({change}:{change?:ConfigurationChange}) {
  if(!change)return null;
  return <section aria-label="待采纳配置来源">
    <p>reasoning effort：{change.settings.before?.value??'未记录'} → {change.settings.after?.value??'未记录'} · {change.settings.effort_changed?'设置值变化':'设置值不变'}</p>
    <p>其他设置、hook 文件及来源身份保持原版本；此候选尚待实际应用。</p>
    {change.files.map(file=><div className="snapshot-asset" key={file.after.path}>
      <p>{file.kind==='user'?'用户配置':'项目配置'} · {file.after.path}</p>
      {(['before','after'] as const).map(side=><div key={side}><p>{side==='before'?'原记录':'待采纳'} · 文件权限 {((file[side].mode??0)&0o777).toString(8).padStart(3,'0')}</p><small className="mono">内容标识 {file[side].sha256}</small></div>)}
    </div>)}
  </section>;
}

function HookSourceDiff({changes}:{changes?:HookSourceChange[]}) {
  if(!changes?.length)return null;
  return <section aria-label="待采纳 hook 文件">{changes.map(change=><div className="snapshot-asset" key={change.source+'/'+(change.after?.path??change.before?.path)}>
    <p>{change.source==='user'?'用户 hook':'项目 hook'} · {change.detail}</p>
    {(['before','after'] as const).map(side=>{const file=change[side];return <div key={side}>
      <p>{side==='before'?'原记录':'待采纳'} · {!file?'不在来源集合中':file.kind==='absent'?'文件不存在':`文件权限 ${((file.mode??0)&0o777).toString(8).padStart(3,'0')}`}</p>
      {(side==='before'?change.before_origin:change.after_origin)?<p className="mono">{side==='before'?change.before_origin:change.after_origin}</p>:null}
      {file?<><p className="mono">{file.path}</p>{file.resolved_path!==file.path?<p className="mono">实际来源：{file.resolved_path}</p>:null}{file.sha256?<small className="mono">内容标识 {file.sha256}</small>:null}</>:null}
    </div>;})}
  </div>)}</section>;
}

function LocalSettingsDiff({changes}: {changes?:LocalChange[]}) {
  if(!changes?.length)return null;
  return <section aria-label="待采纳本地设置文件">{changes.map(change=><div className="snapshot-asset" key={change.before.path}>
    <p className="mono">{change.before.path}</p>
    {(['before','after'] as const).map(side=><div key={side}><p>{side==='before'?'原记录':'待采纳'} · {change[side].kind==='absent'?'不存在':`文件权限 ${((change[side].mode??0)&0o777).toString(8).padStart(3,'0')}`}</p>
      {change[side].sha256?<small className="mono">内容标识 {change[side].sha256}</small>:null}
      {change[side].resolved_path!==change[side].path?<p className="mono">实际路径：{change[side].resolved_path}</p>:null}
    </div>)}
  </div>)}</section>;
}
const mode = (value: string) => value === 'enabled' ? '启用' : value === 'disabled' ? '禁用' : '继承';
const changeText = (change: Change) => change.kind === 'installed_cli_plugin' ? `${change.source_key} · 已安装 CLI 插件：${change.before === 'unrecorded' ? '未单独记录' : mode(change.before)} → ${mode(change.after)}` : `${change.kind === 'skill' || change.kind === 'cli_plugin' ? (change.name ?? change.asset_id) + (change.kind === 'cli_plugin' ? ' · CLI 插件' : ' · Skill') : change.server}：${mode(change.before)} → ${mode(change.after)}${change.before_version !== change.after_version ? `（资源 ${change.before_version ?? '无显式版本'} → ${change.after_version ?? '无显式版本'}）` : ''}${change.before_hook_trust !== change.after_hook_trust ? ` · hook 信任：${change.before_hook_trust === 'trusted' ? '信任' : '未信任'} → ${change.after_hook_trust === 'trusted' ? '信任' : '未信任'}` : ''}`;

export function ExecutionChangeDetails({ sessionId, snapshot, pending, connected, busy, read, command, hold, nativeStatus, sessionStatus, assets, settingsGuard, configurationFailure }: {
  sessionId: string; snapshot: ExecutionSnapshot; pending?: ExecutionChange; connected: boolean; busy: boolean;
  read: <T>(path: string, body?: unknown) => Promise<T>; command: (fields: CommandFields) => Promise<void>;
  hold: boolean; nativeStatus?: string; sessionStatus: string;
  assets: CapabilityAsset[];
  settingsGuard?: SettingsGuard;
  configurationFailure?: ConfigurationFailure;
}) {
  const claude = snapshot.instance.kind === 'claude';
  const installedSettings = pending?.snapshot.claude_installed_plugin_settings;
  const installedBinding = installedSettings?.binding;
  const supportedInstalled = pending?.snapshot.schema_version === 2 && installedBinding?.version === 1 && installedBinding.artifact.id === pending.snapshot.id;
  const unsupportedCandidate = Boolean(pending && (installedSettings || pending.snapshot.schema_version !== undefined && pending.snapshot.schema_version !== 1) && !supportedInstalled);
  const localSettings = claude && snapshot.claude_local_settings?.version===2;
  const [adoptLocalSettings,setAdoptLocalSettings]=useState(Boolean(pending?.adopt_local_settings));
  const hookSources = snapshot.instance.kind==='codex'&&snapshot.codex_user_hooks?.version===1&&snapshot.codex_project_hooks?.version===1;
  const [adoptHookSources,setAdoptHookSources]=useState(Boolean(pending?.adopt_codex_hook_sources));
  const configurationSources = snapshot.instance.kind==='codex'&&snapshot.codex_configuration_sources?.settings?.version===1&&snapshot.codex_configuration_sources.settings.reasoning_effort.state==='set';
  const [adoptConfiguration,setAdoptConfiguration]=useState(Boolean(pending?.adopt_codex_configuration_sources));
  const pendingSources = Boolean(pending?.adopt_codex_hook_sources || pending?.adopt_codex_configuration_sources);
  const hookIds = new Set(snapshot.plugin_selections?.filter(pack => pack.hooks).map(pack => pack.asset_id));
  const hookDeactivation = Boolean(pending?.changes.length && pending.changes.every(change =>
    hookIds.has(change.asset_id) && change.kind === 'cli_plugin' && ['disabled', 'inherit'].includes(change.after) &&
    (change.after === 'inherit' || change.before_version === change.after_version) &&
    !(change.after_hook_trust === 'trusted' && change.before_hook_trust !== 'trusted')) &&
    pending.snapshot.plugin_selections?.every(pack => !pack.hooks || pack.mode === 'disabled') !== false);
  const canCheckChange = !unsupportedCandidate && (sessionStatus === 'idle' || pendingSources && pending?.hook_source_proof?.version===1 && sessionStatus==='unknown' || snapshot.instance.kind === 'codex' && (Boolean(snapshot.skill_selections?.length) || hookDeactivation || pendingSources) && sessionStatus === 'unknown' && nativeStatus === 'idle') && (!pending?.adopt_local_settings || settingsGuard?.state==='registered' && nativeStatus==='sdk_live');
  const backend = snapshot.instance.kind;
  const hasMCPResources = Boolean(snapshot.mcp_selections?.some(s => s.definition) || pending?.snapshot.mcp_selections?.some(s => s.definition) || assets.some(a => a.kind === 'mcp' && !a.codex_mcp && !a.disabled && a.backends.includes(backend)));
  const hasPlugins = Boolean(hasMCPResources || snapshot.skill_selections?.length || snapshot.plugin_selections?.length || pending?.snapshot.skill_selections?.length || pending?.snapshot.plugin_selections?.length || assets.some(a => ['cli_plugin','skill'].includes(a.kind) && !a.disabled && a.backends.includes(backend)));
  const [selections, setSelections] = useState<Selection[]>(() => {
    const target = pending?.snapshot ?? snapshot;
    const picks: Selection[] = [...(target.mcp_selections ?? []), ...(target.skill_selections ?? []), ...(target.plugin_selections ?? [])].map(({ asset_id, mode }) => ({ asset_id, mode,
      ...(claude || target.mcp_selections?.some(s => s.asset_id === asset_id && s.definition) || target.skill_selections?.some(s => s.asset_id === asset_id) || target.plugin_selections?.some(s => s.asset_id === asset_id) ? { version: target.capability_refs.find(r => r.asset.id === asset_id)?.asset.version } : {})
    })).filter((s, i, values) => values.findIndex(v => v.asset_id === s.asset_id) === i);
    for (const old of [...(snapshot.mcp_selections ?? []), ...(snapshot.skill_selections ?? []), ...(snapshot.plugin_selections ?? [])]) if (!picks.some(s => s.asset_id === old.asset_id)) picks.push({asset_id:old.asset_id,mode:'inherit'});
    return picks;
  });
  const pluginChange = !claude && (adoptHookSources || adoptConfiguration || Boolean(snapshot.mcp_selections?.some(s => s.definition) || snapshot.skill_selections?.length || snapshot.plugin_selections?.length || (pending ? pending.snapshot.plugin_selections?.length || pending.snapshot.mcp_selections?.some(s => s.definition) : selections.some(s => assets.some(a => a.id === s.asset_id && (['cli_plugin','skill'].includes(a.kind) || a.kind === 'mcp' && !a.codex_mcp))))));
  const [preview, setPreview] = useState<Preview>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const hookReviewSources = JSON.stringify(assets.filter(asset => asset.kind === 'cli_plugin').map(asset => [asset.id, asset.version, asset.source, asset.disabled]));
  useEffect(() => {
    setSelections(current => current.map(s => ({...s, hook_review: undefined})));
    setPreview(undefined);
  }, [connected, hookReviewSources]);
  const base = { base_snapshot_id: snapshot.id, pending_change_id: pending?.id ?? '' };
  const discardDisabled = !connected || busy || loading || Boolean(pending && pending.status !== 'pending');
  const actionDisabled = discardDisabled || unsupportedCandidate;
  const disabled = actionDisabled || pending?.installed_plugin_change === true;
  async function inspect() {
    setLoading(true); setError(''); setPreview(undefined);
    try { setPreview(await read<Preview>(`/api/sessions/${encodeURIComponent(sessionId)}/execution/preview`, { ...base, selections, adopt_local_settings:adoptLocalSettings, adopt_codex_hook_sources:adoptHookSources, adopt_codex_configuration_sources:adoptConfiguration })); }
    catch (e) { setError((e as Error).message); }
    finally { setLoading(false); }
  }
  return <section className="execution-change" aria-label={localSettings || hookSources || configurationSources ? '原会话配置改配' : (claude || hasPlugins) ? "原会话能力改配" : "原会话 MCP 改配"}>
    <p className="snapshot-subtitle">{localSettings || hookSources || configurationSources ? '原会话配置改配' : (claude || hasPlugins) ? "原会话能力改配" : "原会话 MCP 改配"}</p>
    <p className="hint">{unsupportedCandidate ? pending?.boundary : claude ? '保存后仍使用原配置。准备应用会核对原历史并保持暂停；明确应用时关闭已确认空闲的自有查询，按原 ID 加载新选择。其他会话继续运行。' : pendingSources ? '保存后仍使用原配置。准备应用只核对原历史并保持暂停；明确应用时复核加载状态，按原 ID 加载已保存版本。' : pluginChange ? '保存后仍使用原配置。准备应用只核对原历史并保持暂停；明确应用才取消当前连接订阅，按原 ID 加载新能力版本。其他会话继续运行。' : '保存后仍使用原配置。准备应用会暂停输入与自动恢复；原线程卸载后，需明确应用并核对新版本。工作台不会停止共享后台。'}</p>
    {pending ? <div role="status" className="snapshot-asset">
      <p>{(pending.status === 'applying' || pending.status === 'recovering') ? `候选版本 ${pending.snapshot.revision} · 实际生效待核对` : `待应用版本 ${pending.snapshot.revision} · 尚未生效`}</p>
      <small className="mono">候选快照 {pending.snapshot.id}</small>
      {pending.failure_detail?<p>{pending.failure_detail}</p>:null}
      {pending.recovery_snapshot?<p>恢复版本 {pending.recovery_snapshot.revision} 尚待确认；当前快照和失败候选继续保留。<small className="mono">恢复快照 {pending.recovery_snapshot.id}</small></p>:null}
      <ConfigurationFailureDetails failure={pending.failure??configurationFailure} operation={pending.operation_id} baseline={pending.baseline} connected={connected} />
      {(claude && (pending.adopt_local_settings || installedBinding) || !claude && (pendingSources || Boolean(snapshot.plugin_selections?.length || pending.snapshot.plugin_selections?.length || snapshot.skill_selections?.length || pending.snapshot.skill_selections?.length))) && pending.status==='applying' && pending.operation_id && pending.baseline ? <ExecutionRecoveryDetails backend={backend} sessionId={sessionId} base={snapshot.id} pending={pending.id} operation={pending.operation_id} baseline={pending.baseline} connected={connected} busy={busy} read={read} command={command} failure={pending.failure??configurationFailure} /> : null}
      {pending.changes.map((change) => <p key={`${change.source_key ?? change.asset_id}/${change.server}`}>{changeText(change)}</p>)}
      {installedSettings ? <section aria-label="已安装插件候选依据">
        {!pending.installed_plugin_change ? <p>保留此前的安装选择；以下预览记录来自最近一次安装选择核对。本次配置变化见下方差异。</p> : null}
        <p>登记版本：{installedSettings.preview.entry.registered_version}</p>
        <p>{installedSettings.preview.validation?.preservation?.matches ? '该次安装选择的其他设置与权限规则保留核对一致。' : '该次安装选择的原生保留依据不完整。'}</p>
        <p>{installedSettings.preview.cleanup.state === 'released' ? '核对用临时设置文件已回收。' : '临时设置文件回收状态待核对。'}</p>
        <small>安装选择核对时间：{new Date(installedSettings.preview.at).toLocaleString()}</small>
        <details><summary>保存的来源版本</summary>
          <p className="mono">安装登记：{installedSettings.preview.source_version}</p>
          <p className="mono">安装选择核对时的启动设置：{installedSettings.preview.startup_source_version}</p>
          {installedBinding ? <p className="mono">本候选启动来源：{installedBinding.startup_source_version}</p> : null}
          <p className="mono">声明资源目录：{installedSettings.declaration_resource.path}</p>
          <small className="mono">资源内容标识 {installedSettings.declaration_resource.sha256}</small>
        </details>
        {installedBinding ? <p>保留的安装选择：{installedBinding.selections.map(p => `${p.native_source}（${mode(p.mode)}）`).join('、')}</p> : <p>旧候选缺少执行文件绑定；请撤销后重新核对保存。</p>}
        <p className="hint">来源记录来自保存时的核对；应用时会再检查文件版本和实际加载结果。修改此方案前请先应用或撤销。</p>
      </section> : null}
      <LocalSettingsDiff changes={pending.local_settings_changes} />
      <HookSourceDiff changes={pending.codex_hook_source_changes} />
      <ConfigurationDiff change={pending.codex_configuration_change} />
      {pendingSources ? <p className="hint">{pending.hook_source_proof?.version===1 ? `已保存原配置依据（${new Date(pending.hook_source_proof.at).toLocaleString()}）。重开后会先核对原线程未加载、原历史、未采纳配置及保存时的受管策略；已有依据不代表当前可输入。` : '此候选未保存原配置依据；只能在原连接仍可核对时应用。'}</p> : null}
      {pending.adopt_local_settings && !canCheckChange ? <p className="hint">采纳本地设置需要原查询仍存活、阻止机制已核对可用且执行空闲；当前不能应用。</p> : null}
      {pending.status==='recovering'?<><p>按原配置恢复中 · 结果待核对，自动化仍暂停；不会重复启动。</p><ConfigurationFailureDetails failure={configurationFailure} operation={pending.recovery_operation_id} baseline={pending.baseline} connected={connected} recovery /></>:null}
      {pending.status === 'preparing' ? <p>正在核对原线程与在途执行。</p> : null}
      {pending.status === 'applying' ? <p>正在核对实际配置。回执丢失时保持暂停，不重复应用。</p> : null}
      {pending.status === 'pending' ? <>
        {hookDeactivation && sessionStatus === 'unknown' ? <p className="hint">可核对已保存的禁用或撤去选择；原配置、历史或输入归属无法确认时仍不会应用。</p> : null}
        {claude || pluginChange || !hold || !pending.baseline ? <button disabled={actionDisabled || !canCheckChange} onClick={() => void command({ kind: 'prepare_execution', session_id: sessionId, execution_change: base })}>准备应用（保持暂停）</button> : null}
        {hold && pending.baseline ? <>
          <p>{claude ? '已取得空闲依据；明确应用将关闭此查询并按原 ID 加载新版本。' : pluginChange ? (pendingSources ? '已取得空闲依据；明确应用会复核加载状态，再按原 ID 加载已保存版本。' : '已取得空闲依据；明确应用将取消当前连接订阅，并按原 ID 加载新能力版本。') : nativeStatus === 'notLoaded' ? '原线程已卸载，可明确应用新版本。' : '已取得空闲依据，等待原线程卸载；当前版本不能热切换。'}</p>
          <button disabled={actionDisabled || !canCheckChange || (!claude && !pluginChange && nativeStatus !== 'notLoaded')} onClick={() => void command({ kind: 'apply_execution', session_id: sessionId, execution_change: base })}>应用已保存配置</button>
        </> : null}
      </> : null}
      <button disabled={discardDisabled} onClick={() => void command({ kind: 'discard_execution', session_id: sessionId, execution_change: base })}>撤销待应用选择</button>
    </div> : null}
    {!claude && hasMCPResources ? <p className="hint">MCP 资源可新增、启停、明确升级或恢复继承。新版本以实际启动标识核对；仍运行旧版本或证据缺失时保持待核对，保留原快照和候选。重新启用会核对当前环境引用。</p> : null}
    {selections.map((selection) => {
      const pinned = snapshot.capability_refs.find(r => r.asset.id === selection.asset_id)?.asset;
      const latest = assets.find(a => a.id === selection.asset_id && !a.disabled && (a.kind === 'cli_plugin' || a.kind === 'skill' || a.kind === 'mcp' && !a.codex_mcp) && a.backends.includes(backend));
      const skill = (pinned ?? latest)?.kind === 'skill', plugin = (pinned ?? latest)?.kind === 'cli_plugin';
      const resourceMCP = (pinned ?? latest)?.kind === 'mcp' && !(pinned ?? latest)?.codex_mcp;
      const versioned = claude || plugin || skill || resourceMCP;
      const label = pinned?.name ?? latest?.name ?? selection.asset_id;
      return <div key={selection.asset_id}><label className="native-capability-selection">
      {label} · 待应用选择
      <select data-mcp-mode={skill || plugin ? undefined : selection.asset_id} data-skill-mode={skill ? selection.asset_id : undefined} data-plugin-mode={plugin ? selection.asset_id : undefined} value={selection.mode} disabled={disabled} onChange={(event) => {
        setSelections((current) => current.map((s) => s.asset_id === selection.asset_id ? { ...s, mode: event.target.value, hook_review: event.target.value === 'inherit' ? undefined : s.hook_review, version:!versioned || event.target.value === 'inherit' ? undefined : s.version ?? pinned?.version ?? latest?.version } : s));
        setPreview(undefined); setError('');
      }}><option value="enabled">启用</option><option value="disabled">禁用</option>{versioned ? <option value="inherit">恢复继承</option> : null}</select>
    </label>{versioned && selection.mode !== 'inherit' ? <small>资源版本：{selection.version ?? pinned?.version ?? latest?.version}</small> : null}
    {versioned && pinned && latest && pinned.version !== latest.version && selection.mode !== 'inherit' ? <label className="native-capability-selection">{label} · 资源版本
      <select data-mcp-version={skill || plugin ? undefined : selection.asset_id} data-skill-version={skill ? selection.asset_id : undefined} data-plugin-version={plugin ? selection.asset_id : undefined} value={selection.version ?? pinned.version} disabled={disabled} onChange={event => {
        setSelections(current => current.map(s => s.asset_id === selection.asset_id ? {...s,version:event.target.value,hook_review:undefined} : s)); setPreview(undefined); setError('');
      }}><option value={pinned.version}>保持 {pinned.version}</option><option value={latest.version}>改用 {latest.version}</option></select>
    </label> : null}
    {!claude && plugin && selection.mode !== 'inherit' && latest && latest.version === (selection.version ?? pinned?.version ?? latest.version) ? <PluginHookReview
      key={`${selection.asset_id}/${selection.version}/${latest.source}/${snapshot.instance.id}/${connected}`} asset={latest} instanceId={snapshot.instance.id} disabled={disabled} read={read}
      onChange={review => { setSelections(current => current.map(s => s.asset_id === selection.asset_id ? {...s,hook_review:review} : s)); setPreview(undefined); setError(''); }} /> : null}
    </div>;
    })}
    {claude || hasPlugins ? <>{['mcp','skill','cli_plugin'].map(kind => <label key={kind} className="native-capability-selection">添加 {kind === 'skill' ? 'Skill' : kind === 'cli_plugin' ? 'CLI 插件' : 'MCP'} 资产
      <select aria-label={`添加 ${kind === 'skill' ? 'Skill' : kind === 'cli_plugin' ? 'CLI 插件' : 'MCP'} 资产`} value="" disabled={disabled} onChange={event => {
        const asset = assets.find(a => a.id === event.target.value);
        if (asset) setSelections(current => [...current,{asset_id:asset.id,mode:'enabled',version:asset.version}]);
        setPreview(undefined); setError('');
      }}><option value="">选择已登记资产</option>{assets.filter(a => a.kind === kind && !a.codex_mcp && !a.disabled && a.backends.includes(backend) && !selections.some(s => s.asset_id === a.id)).map(a => <option key={a.id} value={a.id}>{a.name} · {a.version}</option>)}</select>
    </label>)}<p className="hint">新增或替换需明确保存并应用。恢复继承会撤去该资产的显式覆盖，继续沿用原生来源与权限；不等于禁用同名继承能力。{claude ? 'CLI 插件按整包启停；变更包括包内 MCP、Skill、命令、Agent 与 hooks。Skill 选择不会抹除历史中的方法正文，也不替代文件权限。' : '支持本地插件、独立 Skill 资源和已登记映射的原生目录 Skill；原生目录继续使用原资源，其他资源保留独立副本。禁用不会删除历史中的 Skill 正文，也不替代文件权限。取消当前连接订阅不表示全局卸载；工作台不会停止共享后台。'}</p></> : null}
    {localSettings ? <label className="capability-reference"><input type="checkbox" checked={adoptLocalSettings} disabled={disabled} onChange={event=>{setAdoptLocalSettings(event.target.checked);setPreview(undefined);setError('');}} />采纳当前项目/本地设置版本</label> : null}
    {localSettings ? <p className="hint">会先预览当前文件与原记录的差异。保存不生效；明确应用才关闭原查询并按原 ID 加载新设置。旧记录保留，失败或结果未知时不自动回滚或重放。</p> : null}
    {hookSources ? <label className="capability-reference hook-source-choice"><input type="checkbox" checked={adoptHookSources} disabled={disabled} onChange={event=>{setAdoptHookSources(event.target.checked);if(event.target.checked)setAdoptConfiguration(false);setPreview(undefined);setError('');}} /><span>采纳当前用户/项目 hook 文件版本</span></label> : null}
    {hookSources ? <p className="hint">预览文件和来源关系差异，保存候选及原配置依据，不改变当前执行。明确应用才按原 ID 加载；仍加载时需原连接核对，重开后需确认原线程未加载。保留原生信任配置，不自动信任新 hash。</p> : null}
    {configurationSources ? <label className="capability-reference configuration-source-choice"><input type="checkbox" checked={adoptConfiguration} disabled={disabled} onChange={event=>{setAdoptConfiguration(event.target.checked);if(event.target.checked)setAdoptHookSources(false);setPreview(undefined);setError('');}} /><span>采纳当前配置来源版本</span></label> : null}
    {configurationSources ? <p className="hint">预览原记录与当前文件的差异。仅支持已记录的非空 reasoning effort 变更或相同设置的文件版本变化；其他配置、hook 文件和引用资源仍需保持原版本。保存不生效，明确应用后回读原线程设置；失败恢复需还原原文件并核对原线程未加载。</p> : null}
    <button disabled={disabled || selections.length === 0 && !adoptLocalSettings && !adoptHookSources && !adoptConfiguration} onClick={() => void inspect()}>{loading ? '正在预览…' : '预览改配差异'}</button>
    {preview ? <div className="snapshot-asset" role="status">
      <p>候选版本 {preview.next_revision} · 与当前生效快照比较</p>
      {preview.changes.map((change) => <p key={`${change.source_key ?? change.asset_id}/${change.server}`}>{changeText(change)}</p>)}
      <LocalSettingsDiff changes={preview.local_settings_changes} />
      <HookSourceDiff changes={preview.codex_hook_source_changes} />
      <ConfigurationDiff change={preview.codex_configuration_change} />
      <p className="hint">{preview.boundary}</p>
      <button disabled={disabled} onClick={() => void command({ kind: 'stage_execution', session_id: sessionId,
        execution_change: { ...base, selections, adopt_local_settings:adoptLocalSettings, adopt_codex_hook_sources:adoptHookSources, adopt_codex_configuration_sources:adoptConfiguration, preview_hash: preview.preview_hash } })}>保存待应用选择</button>
    </div> : null}
    {error ? <p role="alert" className="error">{error}</p> : null}
  </section>;
}
