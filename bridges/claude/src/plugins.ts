import { createHash } from 'node:crypto';
import { lstatSync, readFileSync, readdirSync, realpathSync, existsSync } from 'node:fs';
import { isAbsolute, join, posix } from 'node:path';
import type { Query } from '@anthropic-ai/claude-agent-sdk';
import { NotSent, record, type SessionConfig } from './protocol.js';
import { skillInventory, type SkillInventory, type SkillSelection } from './skills.js';
import { mcpInventory, mcpToolPrefix, type MCPInventory } from './mcp.js';
import type { NativeHookChannel, hookInventory } from './hook_catalog.js';
import { originalQueryRecoveryMatches, type ConfigurationChange } from './configuration.js';
import { adoptedSettingsHookSources } from './settings_sources.js';

export type PluginFile = { path: string; kind: 'file' | 'directory'; mode: number; sha256?: string };
export type PluginComponent = { kind: 'skill' | 'command' | 'agent' | 'hook' | 'mcp'; name: string; path: string; native_name: string; count: number };
export type PluginSelection = { asset_id: string; name: string; mode: 'enabled' | 'disabled'; version: string;
  source_path: string; source_hash: string; root_mode: number; files: PluginFile[]; components: PluginComponent[]; native_source?: string };
export type PluginPackage = { name: string; path: string; source?: string; version?: string };
export type PluginInventory = { available: boolean; at: string; packages: PluginPackage[];
  skills?: SkillInventory; agents?: { name: string }[]; hooks?: ReturnType<typeof hookInventory>; mcp?: MCPInventory };

const fail = () => new NotSent('CLI 插件来源、版本或组件尚未核对一致，不能接受输入');
const hash = (data: Buffer) => createHash('sha256').update(data).digest('hex');
const id = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_-]+$/.test(value);
const path = (value: unknown): value is string => typeof value === 'string' && !!value && !value.startsWith('/') &&
  !value.includes('\\') && !value.includes('\0') && value !== '.' && value !== '..' && !value.startsWith('../') && posix.normalize(value) === value;

export function pluginSelections(value: unknown, installed = false): PluginSelection[] {
  if (!Array.isArray(value) || value.length > 16) throw fail();
  const names = new Set<string>(), assets = new Set<string>();
  return value.map(raw => {
    const p = record(raw);
    const sourceValid = installed ? p.asset_id === '' && typeof p.native_source === 'string' && /^[A-Za-z0-9_-]{1,100}@[A-Za-z0-9_-]{1,100}$/.test(p.native_source) && p.native_source.split('@')[0] === p.name
      : id(p.asset_id) && !assets.has(p.asset_id) && p.native_source === undefined;
    if (!sourceValid || !id(p.name) || p.name.length > 100 || p.name.startsWith('netcap-skill-') || names.has(p.name) ||
      !['enabled','disabled'].includes(String(p.mode)) || typeof p.version !== 'string' || p.version.length > 100 ||
      typeof p.source_path !== 'string' || !isAbsolute(p.source_path) || p.source_path.includes('\0') ||
      typeof p.source_hash !== 'string' || !/^[a-f0-9]{64}$/.test(p.source_hash) ||
      !Number.isInteger(p.root_mode) || Number(p.root_mode) < 0 || Number(p.root_mode) > 0o777 ||
      !Array.isArray(p.files) || p.files.length > 2048 || !Array.isArray(p.components) || p.components.length > 128) throw fail();
    const paths = new Set<string>();
    for (const raw of p.files) {
      const file = record(raw);
      if (!path(file.path) || paths.has(file.path) || !['file','directory'].includes(String(file.kind)) ||
        !Number.isInteger(file.mode) || Number(file.mode) < 0 || Number(file.mode) > 0o777 ||
        file.kind === 'file' && (typeof file.sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(file.sha256))) throw fail();
      paths.add(file.path);
    }
    for (const raw of p.components) {
      const c = record(raw);
      if (!['skill','command','agent','hook','mcp'].includes(String(c.kind)) || typeof c.name !== 'string' || !c.name ||
        !path(c.path) || !paths.has(c.path) || typeof c.native_name !== 'string' || !c.native_name || c.native_name.length > 300 ||
        !Number.isInteger(c.count) || Number(c.count) < 1 || Number(c.count) > 128) throw fail();
    }
    names.add(p.name); assets.add(String(p.asset_id));
    return p as PluginSelection;
  });
}

export function executionPlugins(config: SessionConfig): PluginSelection[] {
  return [...(config.plugin_selections ?? []), ...(config.installed_settings?.selections ?? [])];
}

const nativeSource = (p: PluginSelection) => p.native_source ?? p.name+'@inline';

export function verifyPluginSources(config: SessionConfig): void {
  for (const p of executionPlugins(config)) {
    if (realpathSync(p.source_path) !== p.source_path) throw fail();
    const root = lstatSync(p.source_path);
    if (!root.isDirectory() || (root.mode & 0o777) !== p.root_mode) throw fail();
    const expected = new Map(p.files.map(file => [file.path, file]));
    let count = 0, bytes = 0;
    const walk = (relative: string) => {
      for (const name of readdirSync(join(p.source_path, relative))) {
        const local = relative ? relative + '/' + name : name, file = expected.get(local), full = join(p.source_path, local), info = lstatSync(full);
        if (++count > 2048 || !file || info.isSymbolicLink() || (!info.isDirectory() && !info.isFile()) ||
          (info.isDirectory() ? 'directory' : 'file') !== file.kind || (info.mode & 0o777) !== file.mode) throw fail();
        if (info.isDirectory()) walk(local);
        else { bytes += info.size; if (bytes > 32*1024*1024 || hash(readFileSync(full)) !== file.sha256) throw fail(); }
      }
    };
    walk(''); if (count !== p.files.length) throw fail();
    const manifest = record(JSON.parse(readFileSync(join(p.source_path, '.claude-plugin/plugin.json'), 'utf8')));
    if (manifest.name !== p.name || (manifest.version ?? '') !== p.version || Object.keys(manifest).some(k =>
      !['name','version','description','author','homepage','repository','license','keywords','skills','commands','agents','hooks','mcpServers'].includes(k))) throw fail();
    if (['.lsp.json','settings.json','output-styles','outputStyles'].some(name => existsSync(join(p.source_path,name)))) throw fail();
    const profile = process.env.CLAUDE_CONFIG_DIR;
    if (!profile || !isAbsolute(profile)) throw fail();
    const registry = join(profile,'plugins/installed_plugins.json');
    if (existsSync(registry)) {
      const packages = record(record(JSON.parse(readFileSync(registry,'utf8'))).plugins);
      const keys = Object.keys(packages).filter(key => key.split('@')[0] === p.name);
      if (p.native_source ? keys.length !== 1 || keys[0] !== p.native_source : keys.length > 0) throw new NotSent(`插件 ${p.name} 的原生安装来源存在冲突`);
    } else if (p.native_source) {
      throw fail();
    }
  }
}

export function selectedPlugins(config: SessionConfig): { type: 'local'; path: string }[] {
  verifyPluginSources(config);
  return (config.plugin_selections ?? []).filter(p => p.mode === 'enabled').map(p => ({ type: 'local', path: p.source_path }));
}

export async function pluginInventory(run: Query | undefined, channel: NativeHookChannel | undefined, packages: PluginPackage[] | undefined, config: SessionConfig): Promise<PluginInventory> {
  const empty: PluginInventory = { available: false, at: new Date().toISOString(), packages: [] };
  if (!run || !channel || !packages) return empty;
  try {
    // This observes loaded packages and their resources. Local settings drift
    // is checked at dispatch and must not hide the live inventory needed for
    // explicit adoption under the existing settings guard.
    verifyPluginSources(config);
    const skills = await skillInventory(run), agents = (await run.supportedAgents()).map(a => ({ name: a.name }));
    const hooks = await channel.read(), mcp = await mcpInventory(run);
    if (!skills.available || !mcp.available || agents.some(a => typeof a.name !== 'string' || !a.name)) return empty;
    return { available: true, at: new Date().toISOString(), packages, skills, agents, hooks, mcp };
  } catch { return empty; }
}

export function pluginsMatch(selections: PluginSelection[], inventory: PluginInventory): boolean {
  if (!selections.length) return true;
  if (!inventory.available || !inventory.skills || !inventory.agents || !inventory.hooks || !inventory.mcp) return false;
  return selections.every(p => {
    const packages = inventory.packages.filter(x => x.name === p.name);
    const commands = inventory.skills!.commands.filter(c => [c.name,...c.aliases].some(n => n.startsWith(p.name+':')));
    const descriptions = inventory.skills!.descriptions.filter(c => c.plugin_name === p.name || c.name.startsWith(p.name+':'));
    const agents = inventory.agents!.filter(a => a.name.startsWith(p.name+':'));
    const hooks = inventory.hooks!.items.filter(h => h.plugin_name === p.name || h.plugin_name.startsWith(p.name+'@'));
    const mcp = inventory.mcp!.servers.filter(s => s.name.startsWith('plugin:'+p.name+':'));
    for (const component of p.components.filter(c => c.kind === 'mcp')) {
      const aliases = inventory.mcp!.servers.filter(server => mcpToolPrefix(server.name) === mcpToolPrefix(component.native_name));
      if (aliases.length !== (p.mode === 'enabled' ? 1 : 0)) return false;
    }
    if (p.mode === 'disabled') return !packages.length && !commands.length && !descriptions.length && !agents.length && !hooks.length && !mcp.length;
    if (packages.length !== 1 || packages[0]!.path !== p.source_path || packages[0]!.source !== nativeSource(p) || (packages[0]!.version ?? '') !== p.version) return false;
    const expected = (kinds: string[]) => p.components.filter(c => kinds.includes(c.kind));
    const exact = (names: string[], components: PluginComponent[]) => names.length === components.reduce((sum,c)=>sum+c.count,0) && components.every(c => names.filter(n=>n===c.native_name).length===c.count);
    if (!exact(commands.map(c=>c.name),expected(['skill','command'])) || !exact(descriptions.map(c=>c.name),expected(['skill','command'])) ||
      !exact(agents.map(a=>a.name),expected(['agent'])) || !exact(hooks.map(h=>h.event),expected(['hook'])) || !exact(mcp.map(s=>s.name),expected(['mcp']))) return false;
    if (descriptions.some(d=>d.source!=='plugin'||d.plugin_name!==p.name) || hooks.some(h=>h.plugin_name!==nativeSource(p)||h.disabled) ||
      mcp.some(s=>s.source!=='plugin'||s.status!=='connected')) return false;
    if (hooks.length && (inventory.hooks!.policy.policy_unreadable || inventory.hooks!.policy.settings_error_count > 0)) return false;
    return commands.every(command => {
      const aliases = new Set([command.name,...command.aliases]);
      return !command.builtin && !inventory.skills!.commands.some(other => other !== command && [other.name,...other.aliases].some(n=>aliases.has(n)));
    });
  });
}

export function pluginChangeMatches(target: PluginSelection[], previous: PluginSelection[], inventory: PluginInventory): boolean {
  // A retired name must disappear from every component namespace, including
  // aliases. A new version of the same name is checked against its exact list.
  const retired = previous.filter(p => !target.some(t => t.name === p.name)).map(p => ({...p, mode:'disabled' as const}));
  return pluginsMatch([...target, ...retired], inventory);
}

export function inheritedPluginComponents(inventory: PluginInventory, plugins: PluginSelection[], skills: SkillSelection[], mcpNames: string[], adoptedHookSources: string[] = []): string {
  const pluginNames = new Set([...plugins.map(p=>p.name), ...skills.flatMap(s=>s.delivery&&s.plugin_name?[s.plugin_name]:[])]);
  const pluginName = (name: string) => [...pluginNames].some(p=>name===p || name.startsWith(p+'@'));
  const commandName = (name: string) => [...pluginNames].some(p=>name.startsWith(p+':')) || skills.some(s=>s.name===name);
  const canonical = (rows: unknown[]) => rows.map(row=>JSON.stringify(row)).sort();
  return JSON.stringify({
    packages: canonical(inventory.packages.filter(p=>!pluginName(p.name))),
    commands: canonical(inventory.skills!.commands.filter(c=>![c.name,...c.aliases].some(commandName))),
    descriptions: canonical(inventory.skills!.descriptions.filter(d=>!commandName(d.name)&&!pluginName(d.plugin_name ?? ''))),
    agents: canonical(inventory.agents!.filter(a=>!commandName(a.name))),
    hooks: canonical(inventory.hooks!.items.filter(h=>!pluginName(h.plugin_name) &&
      // Only explicitly adopted ordinary settings hooks may change here.
      // Unselected plugin hooks, unknown sources and policy stay protected.
      !(!h.plugin_name && ['projectSettings','localSettings'].includes(h.source) && adoptedHookSources.includes(h.source)))),
    policy: inventory.hooks!.policy,
    mcp: canonical(inventory.mcp!.servers.filter(s=>!mcpNames.includes(s.name)&&![...pluginNames].some(p=>s.name.startsWith('plugin:'+p+':')))),
  });
}

export function pluginRecoveryMatches(config: SessionConfig, change: ConfigurationChange, inventory: PluginInventory): boolean {
  if (change.recovery?.original_query_retry && !originalQueryRecoveryMatches(change, change.recovery.original_query_retry)) return false;
  // Removing an installed override restores inheritance. It does not retire
  // that installed package like an inline package removed from the query.
  if (!pluginChangeMatches(executionPlugins(config), change.plugin_target ?? [], inventory)) return false;
  if (!change.installed_target && !change.installed_previous) return true;
  let before = change.original_plugins;
  if (before === undefined) {
    // No previous inventory is invented. This is the first successful check
    // of the unchanged original query, proven to precede any candidate start.
    const complete = (value: PluginInventory | undefined) => value?.available === true && Array.isArray(value.packages) &&
      value.skills?.available === true && Array.isArray(value.skills.commands) && Array.isArray(value.skills.descriptions) &&
      Array.isArray(value.agents) && value.hooks?.available === true && Array.isArray(value.hooks.items) && !!value.hooks.policy &&
      value.mcp?.available === true && Array.isArray(value.mcp.servers);
    if (!originalQueryRecoveryMatches(change, change.recovery?.original_query_retry) || !complete(inventory)) return false;
    if (change.status === 'applying' && change.recovery?.status === 'recovering') return true;
    // Later inputs/reopens compare against the separately saved recovery
    // inventory. It never becomes a fabricated pre-failure observation.
    if (change.status !== 'recovered' || change.recovery?.status !== 'verified' || !complete(change.recovery.plugins)) return false;
    before = change.recovery.plugins;
  }
  if (!before?.available || !inventory.available) return false;
  const controlled = [...executionPlugins(config), ...(change.plugin_target ?? [])];
  const skills = [...(config.skill_selections ?? []), ...(change.skill_target ?? [])];
  const mcp = [...(config.mcp_selections ?? []), ...change.target].map(s => s.server);
  const adopted = adoptedSettingsHookSources(change.settings_previous, change.settings_target);
  return inheritedPluginComponents(before, controlled, skills, mcp, adopted) ===
    inheritedPluginComponents(inventory, controlled, skills, mcp, adopted);
}

export async function verifyPlugins(run: Query | undefined, channel: NativeHookChannel | undefined, packages: PluginPackage[] | undefined, config: SessionConfig, previous: PluginSelection[] = []) {
  const selections = executionPlugins(config);
  if (!selections.length && !previous.length) return;
  verifyPluginSources(config);
  for (let attempt=0;attempt<60;attempt++) {
    const inventory=await pluginInventory(run,channel,packages,config);
    if (pluginChangeMatches(selections,previous,inventory)) return;
    if (!inventory.mcp?.servers.some(s=>s.status==='pending'&&selections.some(p=>s.name.startsWith('plugin:'+p.name+':')))) break;
    await new Promise(resolve=>setTimeout(resolve,100));
  }
  throw fail();
}
