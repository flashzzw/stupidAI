import { lstatSync, mkdirSync, readFileSync, readdirSync, readlinkSync, realpathSync, symlinkSync, writeFileSync } from 'node:fs';
import { dirname, join, parse, resolve } from 'node:path';
import { NotSent, type SessionConfig } from './protocol.js';
import type { SkillSelection } from './skills.js';

function directory(path: string, create = false): void {
  if (create) {
    try { mkdirSync(path, { mode: 0o700 }); } catch (e) { if ((e as NodeJS.ErrnoException).code !== 'EEXIST') throw e; }
  }
  if (!lstatSync(path).isDirectory() || lstatSync(path).isSymbolicLink()) throw new NotSent('Skill 加载目录不是自有普通目录');
}

function ancestors(path: string): void {
  const parts: string[] = [];
  for (let p = resolve(path); p !== parse(p).root; p = dirname(p)) parts.unshift(p);
  for (const p of parts) directory(p);
}

function exactEntries(path: string, expected: string[]): void {
  directory(path);
  if (JSON.stringify(readdirSync(path).sort()) !== JSON.stringify(expected.sort())) throw new NotSent('Skill 加载包出现未登记组件或不完整内容');
}

export function skillPluginRoot(s: SkillSelection): string {
  return dirname(dirname(dirname(s.discovery_path)));
}

function manifest(s: SkillSelection): string {
  return JSON.stringify({ name: s.plugin_name, version: '1.0.0', description: 'Workbench Skill resource binding' }) + '\n';
}

export function verifySkillResource(s: SkillSelection): void {
  if (realpathSync(s.resource_root!) !== s.resource_root || realpathSync(s.source_path) !== s.source_path ||
    !lstatSync(s.source_path).isFile() ||
    (s.resource_kind === 'directory' ? !lstatSync(s.resource_root!).isDirectory() : s.resource_root !== s.source_path)) {
    throw new NotSent('Skill 资源入口已变化');
  }
  if (s.mode === 'disabled') return;
  const root = skillPluginRoot(s);
  ancestors(root);
  exactEntries(root, ['.claude-plugin', 'skills']);
  exactEntries(join(root, '.claude-plugin'), ['plugin.json']);
  const metadata = join(root, '.claude-plugin/plugin.json');
  if (!lstatSync(metadata).isFile() || lstatSync(metadata).isSymbolicLink() || readFileSync(metadata, 'utf8') !== manifest(s)) {
    throw new NotSent('Skill 加载包声明已变化');
  }
  exactEntries(join(root, 'skills'), ['method']);
  const entry = join(root, 'skills/method');
  if (s.resource_kind === 'file') exactEntries(entry, ['SKILL.md']);
  const link = s.resource_kind === 'directory' ? entry : join(entry, 'SKILL.md');
  if (!lstatSync(link).isSymbolicLink() || readlinkSync(link) !== s.resource_root || realpathSync(s.discovery_path) !== s.source_path) {
    throw new NotSent('Skill 加载包链接已变化');
  }
}

// Called only when opening an owned Session, never by preview/capture. The
// wrapper exposes one Skill component; asset-level hooks/MCP/agents are not
// plugin components. A file asset does not expose its unregistered parent.
export function prepareSkillResources(stateRoot: string, config: SessionConfig): { type: 'local'; path: string }[] {
  const plugins: { type: 'local'; path: string }[] = [];
  for (const s of config.skill_selections ?? []) {
    if (s.delivery !== 'local_plugin') continue;
    const parent = join(stateRoot, 'skill-resources', s.plugin_name!);
    const root = join(parent, s.resource_hash!);
    if (s.discovery_path !== join(root, 'skills/method/SKILL.md')) throw new NotSent('Skill 加载包不属于绑定的运行目录');
    if (s.mode === 'disabled') { verifySkillResource(s); continue; }
    ancestors(stateRoot);
    directory(join(stateRoot, 'skill-resources'), true);
    directory(parent, true);
    let fresh = false;
    try { mkdirSync(root, { mode: 0o700 }); fresh = true; } catch (e) { if ((e as NodeJS.ErrnoException).code !== 'EEXIST') throw e; }
    if (fresh) {
      directory(join(root, '.claude-plugin'), true);
      directory(join(root, 'skills'), true);
      writeFileSync(join(root, '.claude-plugin/plugin.json'), manifest(s), { flag: 'wx', mode: 0o600 });
      const entry = join(root, 'skills/method');
      if (s.resource_kind === 'file') directory(entry, true);
      symlinkSync(s.resource_root!, s.resource_kind === 'directory' ? entry : join(entry, 'SKILL.md'));
    }
    verifySkillResource(s);
    plugins.push({ type: 'local', path: root });
  }
  return plugins;
}
