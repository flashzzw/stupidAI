import { realpathSync, statSync, existsSync, readdirSync, type BigIntStats } from 'node:fs';
import { basename, dirname, isAbsolute, join } from 'node:path';
import type { Query, Settings } from '@anthropic-ai/claude-agent-sdk';
import { verifySkillResource } from './skill_resources.js';
import { NotSent, record, type SessionConfig } from './protocol.js';

export type SkillSelection = { asset_id: string; name: string; native_key: string; mode: 'enabled' | 'disabled';
  source: 'userSettings' | 'projectSettings' | 'plugin'; source_path: string; discovery_path: string;
  delivery?: 'local_plugin'; plugin_name?: string; resource_root?: string; resource_kind?: 'file' | 'directory'; resource_hash?: string };
export type SkillInventory = { available: boolean; at: string;
  commands: { name: string; description?: string; builtin: boolean; aliases: string[] }[]; descriptions: { name: string; source: string; plugin_name?: string }[];
  instructions?: { available: boolean; items: { path: string; source: string; tokens: number }[] } };

function instructionSources(value: unknown): NonNullable<SkillInventory['instructions']> {
  const unavailable = { available: false, items: [] };
  if (!Array.isArray(value) || value.length > 256) return unavailable;
  const seen = new Set<string>();
  try {
    const items = value.map(raw => {
      const item = record(raw);
      if (typeof item.path !== 'string' || !isAbsolute(item.path) || item.path.length > 4096 || item.path.includes('\0') ||
        typeof item.type !== 'string' || !item.type || item.type.length > 100 || item.type.includes('\0') ||
        !Number.isSafeInteger(item.tokens) || Number(item.tokens) < 0) throw Error('invalid instruction metadata');
      const key = item.path + '\0' + item.type;
      if (seen.has(key)) throw Error('duplicate instruction source');
      seen.add(key);
      return { path: item.path, source: item.type, tokens: Number(item.tokens) };
    });
    return { available: true, items };
  } catch { return unavailable; }
}

export function skillSelections(value: unknown): SkillSelection[] {
  if (!Array.isArray(value) || value.length > 16) throw new NotSent('invalid Skill selections');
  const keys = new Set<string>(), assets = new Set<string>();
  return value.map(raw => {
    const s = record(raw);
    if (Object.keys(s).some(k => !['asset_id','name','native_key','mode','source','source_path','discovery_path','delivery','plugin_name','resource_root','resource_kind','resource_hash'].includes(k)) ||
      typeof s.asset_id !== 'string' || !s.asset_id || assets.has(s.asset_id) ||
      typeof s.name !== 'string' || !s.name || s.name.length > 200 ||
      typeof s.native_key !== 'string' || !/^[A-Za-z0-9_:-]+$/.test(s.native_key) || keys.has(s.native_key) ||
      !['enabled','disabled'].includes(String(s.mode)) || !['userSettings','projectSettings','plugin'].includes(String(s.source)) ||
      typeof s.source_path !== 'string' || !isAbsolute(s.source_path) || s.source_path.includes('\0') ||
      typeof s.discovery_path !== 'string' || !isAbsolute(s.discovery_path) || s.discovery_path.includes('\0')) throw new NotSent('invalid or duplicate Skill binding');
    if (s.delivery === 'local_plugin') {
      if (s.source !== 'plugin' || typeof s.plugin_name !== 'string' || !/^netcap-skill-[a-f0-9]{24}$/.test(s.plugin_name) ||
        s.native_key !== `${s.plugin_name}:method` || !s.name.startsWith(s.plugin_name + ':') || !/^[A-Za-z0-9_-]+$/.test(s.name.slice(s.plugin_name.length + 1)) ||
        typeof s.resource_root !== 'string' || !isAbsolute(s.resource_root) || s.resource_root.includes('\0') ||
        !['file','directory'].includes(String(s.resource_kind)) || typeof s.resource_hash !== 'string' || !/^[a-f0-9]{64}$/.test(s.resource_hash) ||
        s.source_path !== (s.resource_kind === 'file' ? s.resource_root : join(s.resource_root, 'SKILL.md'))) throw new NotSent('invalid Skill resource delivery');
    } else if (s.source === 'plugin' || !/^[A-Za-z0-9_-]+$/.test(s.native_key) ||
      ['delivery','plugin_name','resource_root','resource_kind','resource_hash'].some(k => k in s)) throw new NotSent('invalid natural Skill binding');
    keys.add(s.native_key); assets.add(s.asset_id);
    return s as SkillSelection;
  });
}

function roots(cwd: string): { path: string; source: string }[] {
  const profile = process.env.CLAUDE_CONFIG_DIR;
  if (!profile || !isAbsolute(profile) || !isAbsolute(cwd)) throw new NotSent('Skill 查询缺少绑定配置目录');
  const result = [{ path: join(profile, 'skills'), source: 'userSettings' }];
  for (let dir = cwd, depth = 0; ; dir = dirname(dir), depth++) {
    if (depth >= 64) throw new NotSent('Skill 项目目录超过核对上限');
    result.push({ path: join(dir, '.claude', 'skills'), source: 'projectSettings' });
    if (dirname(dir) === dir) return result;
  }
}

export function verifySkillSources(config: SessionConfig): void {
  const selections = config.skill_selections ?? [];
  if (!selections.length) return;
  const entries = nativeSkillEntries(config.cwd);
  for (const s of selections) {
    let source: BigIntStats;
    try { source = statSync(s.source_path, { bigint: true }); }
    catch { throw new NotSent('Skill 原始入口的文件身份无法核对'); }
    if (!source.isFile()) throw new NotSent('Skill 原始入口不是普通文件');
    const aliases = entries.filter(entry => entry.identity.dev === source.dev && entry.identity.ino === source.ino);
    if (s.delivery === 'local_plugin') {
      verifySkillResource(s);
      if (aliases.length) throw new NotSent('外部 Skill 新增了原生入口或文件别名，不能独立选配加载包');
      continue;
    }
    const candidates = entries.filter(entry => entry.key === s.native_key);
    if (candidates.length !== 1 || candidates[0]?.path !== s.discovery_path || candidates[0]?.source !== s.source ||
      basename(dirname(s.discovery_path)) !== s.native_key || candidates[0]?.resolved !== s.source_path || aliases.length !== 1 || aliases[0]?.path !== s.discovery_path) {
      throw new NotSent(`Skill ${s.name} 的原生入口变化或存在同目录键/文件别名，无法单独选配此资产`);
    }
  }
}

function nativeSkillEntries(cwd: string) {
  const entries: { path: string; resolved: string; key: string; source: string; identity: BigIntStats }[] = [];
  let count = 0;
  try {
    for (const root of roots(cwd)) {
      if (!existsSync(root.path)) continue;
      for (const child of readdirSync(root.path, { withFileTypes: true })) {
        if (++count > 2048) throw new NotSent('Skill 来源条目超过核对上限');
        if (!child.isDirectory() && !child.isSymbolicLink()) continue;
        const directory = join(root.path, child.name);
        if (!statSync(directory).isDirectory()) continue;
        const path = join(directory, 'SKILL.md');
        if (!existsSync(path)) continue;
        const identity = statSync(path, { bigint: true });
        if (!identity.isFile()) continue;
        entries.push({ path, resolved: realpathSync(path), key: child.name, source: root.source, identity });
      }
    }
  } catch (error) {
    if (error instanceof NotSent) throw error;
    throw new NotSent('Skill 原生来源目录或入口无法完整核对');
  }
  return entries;
}

export async function skillInventory(run?: Query, config?: SessionConfig, includeDescriptions = false): Promise<SkillInventory> {
  const out: SkillInventory = { available: false, at: new Date().toISOString(), commands: [], descriptions: [] };
  if (!run) return out;
  try {
    if (config) verifySkillSources(config);
    const commands = await run.supportedCommands();
    // The default 'full' mode calls the token-count API. Estimates here are
    // only an inventory of frontmatter; they are never method usage evidence.
    const context = await run.getContextUsage({ detail: 'summary' });
    if (!Array.isArray(commands) || context.skills && !Array.isArray(context.skills.skillFrontmatter)) return out;
    // Keep instruction observations out of persisted selection inventories.
    // Paths and local token estimates do not identify the loaded file bytes.
    if (includeDescriptions) out.instructions = instructionSources(context.memoryFiles);
    out.commands = commands.map(c => ({ name: c.name, ...(includeDescriptions ? { description: c.description ?? '' } : {}), builtin: c.builtin === true, aliases: c.aliases ?? [] }));
    out.descriptions = (context.skills?.skillFrontmatter ?? []).map(s => ({ name: s.name, source: s.source, ...(typeof record(s).pluginName === 'string' ? { plugin_name: record(s).pluginName as string } : {}) }));
    out.available = out.commands.every(c => typeof c.name === 'string' && !!c.name && Array.isArray(c.aliases) && c.aliases.every(a => typeof a === 'string' && !!a)) && out.descriptions.every(s => typeof s.name === 'string' && !!s.name && typeof s.source === 'string');
    return out;
  } catch { return out; }
}

export function skillsMatch(selections: SkillSelection[], inventory: SkillInventory): boolean {
  if (!inventory.available) return false;
  return selections.every(s => {
    const commands = inventory.commands.filter(c => s.delivery ? [c.name, ...c.aliases].some(n => n.startsWith(s.plugin_name + ':')) : c.name === s.name || c.aliases.includes(s.name));
    const descriptions = inventory.descriptions.filter(d => s.delivery ? d.plugin_name === s.plugin_name || d.name.startsWith(s.plugin_name + ':') : d.name === s.name);
    const selected = commands[0];
    if (selected) {
      const names = new Set([selected.name, ...selected.aliases]);
      if (inventory.commands.some(c => c !== selected && [c.name, ...c.aliases].some(n => names.has(n)))) return false;
    }
    return s.mode === 'disabled' ? !commands.length && !descriptions.length :
      commands.length === 1 && commands[0]!.name === s.name && !commands[0]!.builtin && descriptions.length === 1 && descriptions[0]!.name === s.name && descriptions[0]!.source === s.source && (!s.delivery || descriptions[0]!.plugin_name === s.plugin_name);
  });
}

export async function verifySkills(run: Query, config: SessionConfig, previous: SkillSelection[] = []): Promise<void> {
  if (!config.skill_selections?.length && !previous.length) return;
  verifySkillSources(config);
  if (!skillChangeMatches(config.skill_selections ?? [], previous, await skillInventory(run, config))) throw new NotSent('Skill 原生命令与描述清单和所选来源不一致；不能确认本次启停');
}

export function skillChangeMatches(target: SkillSelection[], previous: SkillSelection[], inventory: SkillInventory): boolean {
  if (!target.length && !previous.length) return true;
  if (!skillsMatch(target, inventory)) return false;
  // Releasing a wrapper must remove its namespace, including aliases. Natural
  // selections inherit the new query's original settings after flag release.
  return previous.filter(s => s.delivery && !target.some(t => t.plugin_name === s.plugin_name))
    .every(s => skillsMatch([{ ...s, mode: 'disabled' }], inventory));
}

export function inheritedSkills(inventory: SkillInventory, selections: SkillSelection[]): string {
  const affected = (name: string) => selections.some(s => s.name === name || s.delivery && name.startsWith(s.plugin_name + ':'));
  return JSON.stringify({
    commands: inventory.commands.filter(c => !affected(c.name)).map(c => JSON.stringify(c)).sort(),
    descriptions: inventory.descriptions.filter(d => !affected(d.name)).map(d => JSON.stringify(d)).sort(),
  });
}

export async function applySkillSelections(run: Query, config: SessionConfig, overlay: Record<string, unknown>): Promise<void> {
  const selections = config.skill_selections ?? [];
  if (!selections.length) return;
  verifySkillSources(config);
  const before = await skillInventory(run, config);
  if (!before.available) throw new NotSent('Skill 原生清单无法读取，未应用选择');
  for (const s of selections) {
    const names = before.commands.filter(c => c.name === s.name);
    const descriptions = before.descriptions.filter(d => d.name === s.name);
    if (names.length > 1 || names.some(c => c.builtin) || descriptions.length > 1 || descriptions.some(d => d.source !== s.source)) throw new NotSent(`Skill ${s.name} 在原生清单中不唯一或来自其他来源，不能单独选配`);
  }
  const original = overlay.skillOverrides === undefined ? {} : record(overlay.skillOverrides);
  const entries: [string, NonNullable<Settings['skillOverrides']>[string]][] = [];
  for (const [key, value] of Object.entries(original)) {
    if (value !== 'on' && value !== 'off' && value !== 'name-only' && value !== 'user-invocable-only') throw new NotSent('Skill 原有可见性设置无效，未应用选择');
    entries.push([key, value]);
  }
  for (const s of selections.filter(s => !s.delivery)) entries.push([s.native_key, s.mode === 'enabled' ? 'on' : 'off']);
  if (selections.some(s => !s.delivery)) {
    await run.applyFlagSettings({ skillOverrides: Object.fromEntries(entries) });
    await run.reloadSkills();
  }
  const after = await skillInventory(run, config);
  const selected = new Set(selections.map(s => s.name));
  const inherited = (inventory: SkillInventory) => JSON.stringify({
    commands: inventory.commands.filter(c => !selected.has(c.name)).map(c => JSON.stringify(c)).sort(),
    descriptions: inventory.descriptions.filter(d => !selected.has(d.name)).map(d => JSON.stringify(d)).sort(),
  });
  if (!skillsMatch(selections, after) || inherited(before) !== inherited(after)) throw new NotSent('Skill 选择未核对一致，或影响了其他继承命令/描述；本次查询不能接受输入');
}
