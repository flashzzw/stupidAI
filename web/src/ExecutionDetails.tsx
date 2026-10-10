import { ConfigurationFailureDetails } from "./ConfigurationFailureDetails";
import { HookExecutions, type HookExecution } from './HookExecutions';
import { ClaudeSettingsGuard, type SettingsGuard } from './ClaudeSettingsGuard';
import type { ConfigurationFailure } from './ConfigurationFailureDetails';
import { ExecutionSourceCheck } from './ExecutionSourceCheck';
import { ClaudePluginSelection, type PluginEvidence, type PluginSelection } from './ClaudePluginSelection';
import { ExecutionChangeDetails, type ExecutionChange } from './ExecutionChangeDetails';
import { MCPLoginDetails } from './MCPLoginDetails';
import { SkillDiscovery } from './SkillDiscovery';
import { SkillContext } from './SkillContext';
import { ClaudeSkillCatalog } from './ClaudeSkillCatalog';
import { ClaudeInstalledPlugins } from './ClaudeInstalledPlugins';
import { CodexSkillAssets } from './CodexSkillAssets';
import { HookCatalog } from './HookCatalog';
import { ConfigurationCatalog, ConfigurationSourceSnapshot, type ConfigurationSources } from './ConfigurationCatalog';
import { CodexPluginChoice } from './PluginHookReview';
import type { CommandFields } from './PendingAction';

export type CapabilityAsset = {
  id: string; name: string; kind: string; version: string; source: string; backends: string[]; disabled?: boolean; codex_mcp?: string; codex_skill?: string; management?: string;
};
export type CapabilityEvidence = {
  asset_id?: string; server: string; mode: string; runtime_status: string; verified: boolean;
  tools: string[]; used_tools?: string[]; source?: string; plugin_id?: string; at: string; detail: string;
};
export type SkillEvidence = { asset_id: string; name: string; native_key: string; mode: string; source: string;
  command_advertised: boolean; description_listed: boolean; verified: boolean; at: string; detail: string; coverage?: string };
type SourceStamp = { path: string; resolved_path: string; kind: string; sha256?: string };
type ClaudeLocalSettings = { version: number; guard_version?: number; sources: (SourceStamp & { mode?: number })[] };
export type ExecutionSnapshot = {
  restored_from_snapshot_id?: string;
  schema_version?: number;
  claude_installed_plugin_settings?: {
    binding?: {version: number; artifact: {id: string; file: {path: string; sha256: string; mode: number}}; selections: PluginSelection[]; startup_source_version: string};
    declaration_resource: SourceStamp;
    preview: { at: string; source_version: string; startup_source_version: string; setting: { source_key: string; enabled: boolean };
      entry: { registered_version: string }; validation?: { preservation?: { matches: boolean } }; cleanup: { state: string } };
  };
  codex_configuration_sources?: ConfigurationSources;
  revision?: number; previous_snapshot_id?: string;
  id: string; created_at: string; cli_version: string; permission: string; state_namespace: string; cwd: string;
  instance: { id: string; kind: string; policy_revision: string };
  route: { id: string; model: string; config_revision: string; credential_ref: string; credential_revision: string };
  profile_source: SourceStamp; settings_source?: SourceStamp; capability_refs: { asset: CapabilityAsset; source: SourceStamp }[];
  claude_local_settings?: ClaudeLocalSettings;
  codex_user_hooks?: { version: number; file: SourceStamp & { mode?: number } };
  codex_project_hooks?: { version: number; files: { file: SourceStamp & { mode?: number }; configuration_layer: boolean; layer_disabled: boolean; native_discovered: boolean }[] };
  mcp_selections?: { asset_id: string; server: string; mode: string }[];
  plugin_selections?: PluginSelection[];
  skill_selections?: { asset_id: string; name: string; native_key: string; mode: string; source: string; source_path: string; discovery_path: string; delivery?: string; plugin_name?: string; resource_kind?: string }[];
};
const assetKinds: Record<string, string> = { skill: 'Skill', mcp: 'MCP 定义', cli_plugin: 'CLI 插件' };

export function CapabilityReferences({ assets, backend, instanceId, connected, read }: { assets: CapabilityAsset[]; backend: string; instanceId: string; connected: boolean; read: <T>(path: string, body?: unknown) => Promise<T> }) {
  const available = assets.filter((asset) => asset.backends.includes(backend));
  if (!available.length) return null;
  return <fieldset className="capability-references">
    <legend>记录能力引用</legend>
    <p className="hint">记录本次依赖的来源与版本，用于核对变化。勾选不改变 CLI 的安装、加载或权限；未勾选也不会禁用原生能力。</p>
    {available.map((asset) => <div key={asset.id}><label className="capability-reference">
      <input type="checkbox" name="capability_ref" value={asset.id} disabled={asset.disabled} />
      <span>{asset.name} · {asset.version}<small>{assetKinds[asset.kind] ?? asset.kind} · {asset.management === 'managed' ? '平台管理 · 固定版本' : '外部管理'}{asset.disabled ? ' · 已停止新引用' : ''}</small></span>
    </label>{(backend === 'codex' && asset.codex_mcp || backend === 'claude' && asset.kind === 'mcp' && !asset.codex_mcp) ? <label className="native-capability-selection">
      {asset.name} · 原生 MCP 选择
      <select name={`capability_selection:${asset.id}`} defaultValue="inherit" disabled={asset.disabled}>
        <option value="inherit">继承原生配置</option><option value="enabled">本次启用</option><option value="disabled">本次禁用</option>
      </select>
      <small>启用或禁用会自动记录来源引用。启用将连接所选 MCP 服务；调用仍受原生权限控制。其他 MCP 继续继承。同名继承能力无法排除时会明确拒绝。</small>
    </label> : null}{['claude', 'codex'].includes(backend) && asset.kind === 'skill' ? <label className="native-capability-selection">
      {asset.name} · 原生 Skill 选择
      <select name={`capability_selection:${asset.id}`} defaultValue="inherit" disabled={asset.disabled}>
        <option value="inherit">继承原生配置</option><option value="enabled">本次启用</option><option value="disabled">本次禁用</option>
      </select>
      <small>{backend === 'codex' ? asset.codex_skill ? '按登记的原生入口选择用户/项目 Skill，并核对完整资源。外部文件变化会阻止继续输入，旧版本不会自动升级。' : '已登记、尚未由原生目录发现的 Skill 资源通过独立副本加载，保留正文与相对资料。已有原生入口需先在资产中登记准确的 Codex 原生 Skill 路径。' : '已有的用户/项目 Skill 入口按原生目录键选配；其他已登记资源通过独立 Skill 包加载，并保存资源版本。名称或别名冲突时拒绝。'}其他 Skill 继续继承，禁用不抹除历史或撤销文件权限。</small>
    </label> : null}{backend === 'codex' && asset.kind === 'cli_plugin' ? <CodexPluginChoice key={`${asset.id}/${asset.version}/${asset.source}/${instanceId}`} asset={asset} instanceId={instanceId} disabled={!connected} read={read} /> : backend === 'claude' && asset.kind === 'cli_plugin' ? <label className="native-capability-selection">
      {asset.name} · CLI 插件选择
      <select name={`capability_selection:${asset.id}`} defaultValue="inherit" disabled={asset.disabled}>
        <option value="inherit">继承原生配置</option><option value="enabled">本次启用整包</option><option value="disabled">本次禁用整包</option>
      </select>
      <small>本地插件整包加载并核对 Skill、命令、Agent、hook 与 MCP。同名已安装来源或无法核对的组件会拒绝。</small>
    </label> : null}</div>)}
  </fieldset>;
}

function SourceDetails({ source, absentLabel = '启动时不存在' }: { source: SourceStamp; absentLabel?: string }) {
  return <><p className="mono">{source.path}</p>
    {source.path !== source.resolved_path ? <p className="mono">实际来源：{source.resolved_path}</p> : null}
    <small className="mono">{source.kind === 'absent' ? absentLabel : `内容标识 ${source.sha256}`}</small></>;
}

function LocalSettingsSources({ snapshot }: { snapshot: ExecutionSnapshot }) {
  if (snapshot.instance.kind !== 'claude') return null;
  const local = snapshot.claude_local_settings;
  return <details className="local-settings-sources"><summary>Claude 项目/本地设置版本</summary>
    {!local ? <p className="hint">此快照未记录项目和本地设置版本；不会补造历史来源。</p> : ![1,2].includes(local.version) || !Array.isArray(local.sources) ? <p className="error">本地设置版本格式尚未核对。</p> : <>
      {local.sources.map(source => <div className="snapshot-asset" key={source.path}>
        <SourceDetails source={source} absentLabel="记录时不存在" />
        {source.kind === 'file' && source.mode !== undefined ? <small className="mono">记录的文件权限：{(source.mode & 0o777).toString(8).padStart(3, '0')}</small> : null}
      </div>)}
      <p className="hint">文件内容、权限、路径或存在状态变化会阻止继续输入。恢复原文件后可重新核对；这里不会自动更新记录。</p>
      <p className="hint">这些是创建时的文件摘要，不代表原生实际加载、全部有效设置或权限已核对，也不冻结原生文件。</p>
    </>}
  </details>;
}

function CodexUserHookSource({ snapshot }: { snapshot: ExecutionSnapshot }) {
  if (snapshot.instance.kind !== 'codex') return null;
  const saved = snapshot.codex_user_hooks;
  return <details className="codex-user-hook-source"><summary>Codex 用户 hook 文件版本</summary>
    {!saved ? <p className="hint">此快照未记录用户 hook 文件版本；不会补造历史来源。</p> : saved.version !== 1 || !saved.file ? <p className="error">用户 hook 文件版本格式尚未核对。</p> : <>
      <SourceDetails source={saved.file} absentLabel="记录时不存在" />
      {saved.file.kind === 'file' && saved.file.mode !== undefined ? <p className="mono">记录的文件权限：{(saved.file.mode & 0o777).toString(8).padStart(3, '0')}</p> : null}
      <p className="hint">此文件的内容、权限、路径或存在状态变化会阻止继续输入和恢复。恢复原文件后可重新核对；记录不会自动升级。</p>
      <p className="hint">只记录用户配置目录的 hooks.json 文件；不表示已加载或已执行，也不覆盖项目、受管 hook 和引用脚本，不冻结原生文件。</p>
    </>}
  </details>;
}

function CodexProjectHookSources({ snapshot }: { snapshot: ExecutionSnapshot }) {
  if (snapshot.instance.kind !== 'codex') return null;
  const saved = snapshot.codex_project_hooks;
  return <details className="codex-project-hook-sources"><summary>Codex 项目 hook 文件版本</summary>
    {!saved ? <p className="hint">此快照未记录项目 hook 文件版本；不会补造历史来源。</p> : saved.version !== 1 || !Array.isArray(saved.files) ? <p className="error">项目 hook 文件版本格式尚未核对。</p> : <>
      {saved.files.length === 0 ? <p className="hint">记录时未发现项目配置层或项目 hook 文件来源。</p> : saved.files.map(({ file, configuration_layer, layer_disabled, native_discovered }) => <div className="snapshot-asset" key={file.path}>
        <SourceDetails source={file} absentLabel="记录时不存在" />
        {file.kind === 'file' && file.mode !== undefined ? <p className="mono">记录的文件权限：{(file.mode & 0o777).toString(8).padStart(3, '0')}</p> : null}
        <p>{configuration_layer ? `配置层声明${layer_disabled ? ' · 该层未启用' : ''}` : '未由配置层声明'}；{native_discovered ? '原生清单发现' : '原生清单未发现'}</p>
      </div>)}
      <p className="hint">文件变化会阻止继续输入和恢复，记录不会自动升级。离线核对只检查已记录文件；连接原生后还会复核来源集合与配置层。</p>
      <p className="hint">配置层声明与原生发现可能指向不同位置。发现不表示已加载或已执行；受管 hook 和引用脚本仍未覆盖，也不冻结原生文件。</p>
    </>}
  </details>;
}

export function ExecutionDetails({ sessionId, generation, snapshot, evidence, skillEvidence, pluginEvidence, hookExecutions, settingsGuard, configurationFailure, connected, read, pending, busy, command, hold, history, attempts, nativeStatus, sessionStatus, assets }: {
  sessionId: string; generation: number; snapshot?: ExecutionSnapshot; connected: boolean;
  evidence?: CapabilityEvidence[];
  skillEvidence?: SkillEvidence[];
  pluginEvidence?: PluginEvidence[];
  hookExecutions?: HookExecution[];
  settingsGuard?: SettingsGuard;
  configurationFailure?: ConfigurationFailure;
  read: <T>(path: string, body?: unknown) => Promise<T>;
  pending?: ExecutionChange; busy: boolean; command: (fields: CommandFields) => Promise<void>;
  assets: CapabilityAsset[]; hold: boolean; history?: ExecutionSnapshot[]; attempts?:{change:ExecutionChange;recovered_at:string}[]; nativeStatus?: string; sessionStatus: string;
}) {
  const installed = snapshot?.claude_installed_plugin_settings?.binding;
  const plugins = [...(snapshot?.plugin_selections?.filter(item => !item.skill_asset) ?? []), ...(installed?.selections ?? [])];
  const skillAssets = snapshot?.plugin_selections?.filter(item => item.skill_asset) ?? [];
  return <details className="execution-snapshot"><summary>执行配置快照</summary>
    {snapshot ? <>
      <p className="hint">此处展示最后确认的执行版本，旧版本保留在下方历史中；来源文件仍可能变化，尚未核对全部配置继承和权限合并。</p>
      {snapshot.instance.kind === 'codex' && nativeStatus === 'notLoaded' && !pending && attempts?.some(attempt => attempt.change.base_snapshot_id === snapshot.id && attempt.change.status === 'recovered') ? <p role="status" className="hint">原配置恢复已确认；原线程当前未加载，能力仍待核对。请明确恢复自动化后继续。</p> : null}
      <dl><dt>快照 ID</dt><dd className="mono">{snapshot.id}</dd>
        <dt>生效版本</dt><dd>{snapshot.revision ?? 1}</dd>
        <dt>记录时间</dt><dd>{new Date(snapshot.created_at).toLocaleString()}</dd>
        <dt>CLI 与策略版本</dt><dd>{snapshot.cli_version} · {snapshot.instance.policy_revision}</dd>
        <dt>路由配置版本</dt><dd>{snapshot.route.id} · {snapshot.route.config_revision}</dd>
        <dt>凭据引用 / 版本</dt><dd className="mono">{snapshot.route.credential_ref} / {snapshot.route.credential_revision}</dd>
        <dt>申请权限</dt><dd>{snapshot.permission === 'read_only' ? '只读 · 按需审批' : '工作区写入 · 按需审批'}</dd></dl>
      <p>CLI 默认配置来源</p><SourceDetails source={snapshot.profile_source} />
      <LocalSettingsSources snapshot={snapshot} />
      <CodexUserHookSource snapshot={snapshot} />
      <CodexProjectHookSources snapshot={snapshot} />
      {snapshot.instance.kind === 'claude' ? <ClaudeSettingsGuard version={snapshot.claude_local_settings?.guard_version} evidence={settingsGuard} connected={connected && nativeStatus === 'sdk_live'} /> : null}
      {snapshot.instance.kind === 'codex' ? <SkillContext key={`skill-context/${snapshot.id}/${connected}`} sessionId={sessionId} connected={connected} read={read} /> : null}
      {snapshot.instance.kind === 'codex' ? <SkillDiscovery key={`skill-discovery/${snapshot.id}/${connected}`} sessionId={sessionId} connected={connected} read={read} /> : null}
      {snapshot.instance.kind === 'claude' ? <ClaudeSkillCatalog key={`claude-skill-catalog/${snapshot.id}/${connected}`} sessionId={sessionId} connected={connected} read={read} /> : null}
      {snapshot.instance.kind === 'claude' ? <ClaudeInstalledPlugins key={`installed/${snapshot.id}/${generation}/${connected}`} sessionId={sessionId} baseSnapshotId={snapshot.id} hasPendingChange={Boolean(pending)} busy={busy} connected={connected} read={read} /> : null}
      {snapshot.instance.kind === 'claude' || snapshot.instance.kind === 'codex' ? <HookExecutions executions={hookExecutions} backend={snapshot.instance.kind} connected={connected && sessionStatus !== 'unknown'} /> : null}
      {snapshot.instance.kind === 'claude' || snapshot.instance.kind === 'codex' ? <HookCatalog key={`hooks/${snapshot.id}/${connected}`} sessionId={sessionId} backend={snapshot.instance.kind} connected={connected} read={read} /> : null}
      {snapshot.instance.kind === 'codex' ? <><ConfigurationSourceSnapshot sources={snapshot.codex_configuration_sources} /><ConfigurationCatalog key={`configuration/${snapshot.id}/${connected}`} sessionId={sessionId} connected={connected} read={read} saved={snapshot.codex_configuration_sources} /></> : null}
      {snapshot.settings_source ? <><p>Claude 自定义 settings 来源</p><SourceDetails source={snapshot.settings_source} /></> : null}
      {installed ? <details className="snapshot-asset"><summary>本执行版本的插件设置文件</summary>
        {snapshot.restored_from_snapshot_id ? <><p>此恢复版本保持原配置内容，重新记录已还原本地文件的身份；原快照保存在历史中。</p><small className="mono">原快照 {snapshot.restored_from_snapshot_id}</small></> : null}
        <p>随执行版本保留，原 ID 重开时核对后使用。原设置来源保持不变。</p>
        <p className="mono">{installed.artifact.file.path}</p>
        <small className="mono">文件内容标识 {installed.artifact.file.sha256} · 权限 0600</small>
        <p className="mono">本版本启动来源 {installed.startup_source_version}</p>
      </details> : null}
      <p className="snapshot-subtitle">能力引用 · {snapshot.capability_refs.length} 项</p>
      {snapshot.capability_refs.length ? snapshot.capability_refs.map(({ asset, source }) => <div className="snapshot-asset" key={asset.id}>
        <p>{asset.name} · {asset.version}</p><small>{assetKinds[asset.kind] ?? asset.kind} · 已引用{[...(snapshot.mcp_selections ?? []), ...(snapshot.skill_selections ?? []), ...(snapshot.plugin_selections ?? [])].some((s) => s.asset_id === asset.id) ? ' · 原生选配见下方' : ' · 此引用不验证加载'}</small><SourceDetails source={source} />
      </div>) : <p className="hint">没有登记能力引用；不代表 CLI 没有加载原生或继承能力。</p>}
      {snapshot.mcp_selections?.length ? <>
        <p className="snapshot-subtitle">本次原生 MCP 选择</p>
        {snapshot.mcp_selections.map((selection) => <p key={`${selection.asset_id}/${selection.server}`}>{selection.server} · {selection.mode === 'enabled' ? '启用' : '禁用'}</p>)}
        <p className="hint">以下是最近保存的原生会话观察。离线或连接变化后需重新核对；来源文件检查不会刷新这份加载证据。</p>
        {evidence?.length ? evidence.filter((item) => item.mode !== 'inherit' && item.mode !== 'history').map((item) => <NativeEvidence key={nativeEvidenceKey(item)} item={item} />) : <p className="hint">等待原生加载核对；尚不能确认本次选择已生效。</p>}
        {evidence?.some((item) => item.mode === 'inherit') ? <><p className="snapshot-subtitle">其他继承的 MCP</p>{evidence.filter((item) => item.mode === 'inherit').map((item) => <NativeEvidence key={item.server} item={item} />)}</> : null}
      </> : null}
      {plugins.length ? <ClaudePluginSelection selections={plugins} evidence={pluginEvidence} connected={connected} backend={snapshot.instance.kind} /> : null}
      {skillAssets.length ? <CodexSkillAssets selections={skillAssets} evidence={pluginEvidence} connected={connected} assets={snapshot.capability_refs.map(ref => ref.asset)} /> : null}
      {snapshot.skill_selections?.length ? <section aria-label="本次原生 Skill 选择">
        <p className="snapshot-subtitle">本次原生 Skill 选择</p>
        {snapshot.skill_selections.map(selection => <div className="snapshot-asset" key={selection.asset_id}>
          <p>{selection.plugin_name ? selection.name.slice(selection.plugin_name.length + 1) : selection.name} · {selection.mode === 'enabled' ? '启用' : '禁用'}</p>
          <small>原生目录键：{selection.native_key} · {selection.delivery === 'native_directory' ? 'Codex 原生目录映射' : selection.source === 'userSettings' ? '用户来源' : selection.source === 'plugin' ? '本次 Skill 资源包' : '项目来源'}</small>
          <p className="mono">原生入口：{selection.discovery_path}</p>
          {selection.delivery ? <><p className="mono">资源来源：{selection.source_path}</p><small>{selection.resource_kind === 'file' ? '单文件入口' : '目录资源入口'} · {selection.delivery === 'native_directory' ? '使用原生目录资源，未复制为插件' : selection.mode === 'enabled' ? '为本会话加载独立 Skill 包' : '本会话省略该 Skill 包'}</small></> : null}
          {!skillEvidence?.some(item => item.asset_id === selection.asset_id) ? <p className="hint">等待原生命令与描述清单核对。</p> : null}
          {skillEvidence?.filter(item => item.asset_id === selection.asset_id).map(item => <div key={item.asset_id}>
            <p>{!connected ? '连接已断开，选择待核对' : item.verified ? item.coverage === 'directory_source_and_connection_configuration' ? '原生来源与本连接 Skill 配置已核对' : '原生命令与描述清单匹配' : '原生选择待核对'}</p><small>{item.detail}</small>
            {item.coverage !== 'directory_source_and_connection_configuration' ? <p>命令清单：{item.command_advertised ? '包含' : '未报告'} · 描述清单：{item.description_listed ? '包含' : '未报告'}</p> : <p>原生来源类别：{item.source === 'user' ? '用户' : item.source === 'repo' ? '项目' : '待核对'}</p>}
            <small>核对时间：{item.at ? new Date(item.at).toLocaleString() : '尚未采样'}</small>
          </div>)}
        </div>)}
        <p className="hint">清单匹配不代表正文已加载、方法已使用或 Gate 已通过。可在下方保存并应用 Skill 改配；关闭查询后按原 ID 恢复会重用保存的选择。</p>
      </section> : null}
      {snapshot.instance.kind === 'claude' ? <><p className="hint">Claude 支持 stdio 与 HTTP/SSE MCP 的启动选择、原 ID 恢复及新增、显式资源版本替换、启停与恢复继承。OAuth 登录命令可在下方准备，原生 CLI 负责授权回调、令牌保管与刷新。</p><MCPLoginDetails key={`${snapshot.id}/${pending?.id??''}`} sessionId={sessionId} snapshot={snapshot} pending={pending?.snapshot} connected={connected} read={read}/></> : null}
      {snapshot.instance.kind === 'claude' || snapshot.instance.kind === 'codex' ? <ExecutionChangeDetails
        key={`${snapshot.id}/${pending?.id ?? ''}`} sessionId={sessionId} snapshot={snapshot} pending={pending}
        assets={assets} connected={connected} busy={busy} read={read} command={command} hold={hold} nativeStatus={nativeStatus} sessionStatus={sessionStatus} settingsGuard={settingsGuard} configurationFailure={configurationFailure} /> : null}
      {!snapshot.mcp_selections?.length && evidence?.some(item => item.mode !== 'inherit' && item.mode !== 'history') ? <section aria-label="插件带入的 MCP"><p className="snapshot-subtitle">插件带入的 MCP</p>{evidence.filter(item => item.mode !== 'inherit' && item.mode !== 'history').map(item => <NativeEvidence key={nativeEvidenceKey(item)} item={item} />)}</section> : null}
      {!snapshot.mcp_selections?.length && evidence?.some(item => item.mode === 'inherit') ? <section aria-label="继承的 MCP"><p className="snapshot-subtitle">继承的 MCP</p>{evidence.filter(item => item.mode === 'inherit').map(item => <NativeEvidence key={item.server} item={item} />)}</section> : null}
      {evidence?.some(item => item.mode === 'history') ? <section aria-label="历史 MCP 来源"><p className="snapshot-subtitle">历史 MCP 来源</p>{evidence.filter(item => item.mode === 'history').map(item => <NativeEvidence key={nativeEvidenceKey(item)} item={item} />)}</section> : null}
      {attempts?.length?<section aria-label="未生效的配置尝试"><p className="snapshot-subtitle">未生效的配置尝试</p>{attempts.map(attempt=><details key={attempt.change.id}><summary>候选版本 {attempt.change.snapshot.revision} · 已恢复原配置</summary><p className="mono">{attempt.change.snapshot.id}</p><p>恢复时间：{new Date(attempt.recovered_at).toLocaleString()}</p>{attempt.change.failure_detail?<p>{attempt.change.failure_detail}</p>:null}<ConfigurationFailureDetails failure={attempt.change.failure} operation={attempt.change.operation_id} baseline={attempt.change.baseline} connected={connected} historical/><p className="mono">恢复操作：{attempt.change.recovery_operation_id}</p><details><summary>完整候选快照与差异</summary><pre>{JSON.stringify(attempt.change,null,2)}</pre></details></details>)}</section>:null}
      {history?.length ? <section aria-label="历史执行快照"><p className="snapshot-subtitle">历史执行快照 · {history.length} 项</p>
        {history.map((old) => <details key={old.id}><summary>历史版本 {old.revision ?? 1}</summary>
          <p className="mono">{old.id}</p><p>记录时间：{new Date(old.created_at).toLocaleString()}</p>
          {old.capability_refs.map(({asset}) => <p key={asset.id}>{asset.name} · 资源 {asset.version}</p>)}
          {old.mcp_selections?.map((selection) => <p key={`${selection.asset_id}/${selection.server}`}>{selection.server} · {selection.mode === 'enabled' ? '启用' : '禁用'}</p>)}
          {old.skill_selections?.map(selection => <p key={selection.asset_id}>{selection.name} · Skill {selection.mode === 'enabled' ? '启用' : '禁用'}</p>)}
          {old.plugin_selections?.map(selection => <p key={selection.asset_id}>{selection.name} · 插件 {selection.version || '未声明版本'} · {selection.mode === 'enabled' ? '启用' : '禁用'}</p>)}
          <SourceDetails source={old.profile_source} />
          <LocalSettingsSources snapshot={old} />
          {old.instance.kind === 'codex' ? <ConfigurationSourceSnapshot sources={old.codex_configuration_sources} /> : null}
        </details>)}
      </section> : null}
      <ExecutionSourceCheck key={`execution-sources/${snapshot.id}/${connected}`} sessionId={sessionId} snapshotId={snapshot.id} connected={connected} read={read} />
    </> : <p className="hint">此会话没有启动快照。旧记录、观察来源及未覆盖的执行类型不会补造历史记录。</p>}
  </details>;
}

function nativeEvidenceKey(item: CapabilityEvidence) { return JSON.stringify([item.server, item.mode, item.source, item.plugin_id]); }

function NativeEvidence({ item }: { item: CapabilityEvidence }) {
  const status: Record<string, string> = { connected: '已连接', disabled: '已禁用', connecting: '连接中', pending: '连接中', 'needs-auth': '需要认证', failed: '连接失败' };
  const sources: Record<string, string> = { dynamic: '本次选择', user: '用户配置', project: '项目配置', local: '项目本地配置', managed: '受管配置', enterprise: '企业配置', plugin: 'CLI 插件', configuration: '原生配置', claudeai: 'Claude 账号连接', agent: '子代理配置', sdk: 'SDK 内部服务', unknown: '未知来源' };
  return <div className="snapshot-asset">
    <p>{item.server} · {item.mode === 'history' ? '本次状态未核对' : status[item.runtime_status] ?? (item.runtime_status || '未知')} · {item.mode === 'history' ? '仅历史记录' : item.mode === 'inherit' ? '继承配置' : item.verified ? '与选择一致' : '未核对一致'}</p>
    {item.asset_id ? <p>绑定能力：{item.asset_id}</p> : null}
    {item.source ? <p>原生来源：{sources[item.source] ?? '未知来源'}</p> : null}
    {item.plugin_id ? <p className="mono">原生插件 ID：{item.plugin_id}</p> : null}
    <small>{item.detail}</small>
    {item.mode === 'history' ? <p>此历史来源的当前工具清单未核对。</p> : <><p>原生工具清单：{item.tools.join('、') || '无'}</p><small>清单不代表调用已获准；调用仍受原生权限规则约束。</small></>}
    <p>历史成功调用：{item.used_tools?.join('、') || '暂无证据'}</p>
    <small>原生核对时间：{item.at ? new Date(item.at).toLocaleString() : '尚未采样'}</small>
  </div>;
}
