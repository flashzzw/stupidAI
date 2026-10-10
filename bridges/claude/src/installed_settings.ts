import { createHash } from 'node:crypto';
import { closeSync, constants, fstatSync, lstatSync, openSync, readFileSync, realpathSync, statSync, type BigIntStats } from 'node:fs';
import { isAbsolute, normalize } from 'node:path';
import { fingerprint } from './configuration.js';
import { pluginSelections, verifyPluginSources, type PluginSelection } from './plugins.js';
import { fileSource, settingsFileArtifact, type FileSource, type SettingsFileArtifact } from './settings_file_consumer.js';
import { NotSent, record, type SessionConfig } from './protocol.js';
import { verifySettingsSources, type SettingsSource } from './settings_sources.js';

type Source = FileSource | {path: string; kind: 'absent'};
type StartupFile = {path: string; status: 'read' | 'absent'; sha256?: string; bytes?: number; resolved_path?: string; identity_sha256?: string};
export type InstalledSettings = {
  version: 1; artifact: SettingsFileArtifact; selections: PluginSelection[];
  source_files: Source[]; startup_files: StartupFile[]; startup_source_version: string;
};
const fail = () => new NotSent('已安装插件的执行设置或登记来源未核对一致');

export function installedSettings(input: unknown): InstalledSettings {
  const v = record(input);
  if (v.version !== 1 || typeof v.startup_source_version !== 'string' || !/^[a-f0-9]{64}$/.test(v.startup_source_version) ||
      !Array.isArray(v.source_files) || !v.source_files.length || v.source_files.length > 2048 ||
      !Array.isArray(v.startup_files) || !v.startup_files.length || v.startup_files.length > 128) throw fail();
  const paths = new Set<string>();
  const sources: Source[] = v.source_files.map(raw => {
    const s = record(raw);
    if (typeof s.path !== 'string' || !isAbsolute(s.path) || normalize(s.path) !== s.path || paths.has(s.path)) throw fail();
    paths.add(s.path);
    return s.kind === 'absent' ? {path:s.path,kind:'absent'} : fileSource(s);
  });
  const selections = pluginSelections(v.selections, true);
  if (!selections.length) throw fail();
  const startupPaths = new Set<string>();
  const startup = v.startup_files.map(raw => {
    const s = record(raw);
    if (typeof s.path !== 'string' || !isAbsolute(s.path) || normalize(s.path) !== s.path || startupPaths.has(s.path) || !['read','absent'].includes(String(s.status))) throw fail();
    if (s.status === 'read' && (typeof s.sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(s.sha256) ||
        typeof s.identity_sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(s.identity_sha256) || typeof s.resolved_path !== 'string' || !isAbsolute(s.resolved_path) ||
        !Number.isSafeInteger(s.bytes) || Number(s.bytes) < 0 || Number(s.bytes) > 1 << 20)) throw fail();
    startupPaths.add(s.path);
    return s as StartupFile;
  });
  return {version:1,artifact:settingsFileArtifact(v.artifact),selections,source_files:sources,startup_files:startup,startup_source_version:v.startup_source_version};
}

const key = (s: BigIntStats) => [s.dev,s.ino,s.mode,s.uid,s.gid,s.nlink,s.size,s.mtimeNs,s.ctimeNs].map(String).join(':');
const hash = (body: Buffer) => createHash('sha256').update(body).digest('hex');

function verifySource(source: Source): void {
  if (source.kind === 'absent') {
    try { lstatSync(source.path); } catch (error) { if ((error as NodeJS.ErrnoException).code === 'ENOENT') return; }
    throw fail();
  }
  const fd = openSync(source.path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try {
    const before = fstatSync(fd,{bigint:true});
    if (!before.isFile() || before.size > 2n << 20n || Number(before.mode & 0o7777n) !== source.mode || realpathSync(source.path) !== source.resolved_path) throw fail();
    const body = readFileSync(fd), after = fstatSync(fd,{bigint:true}), named = lstatSync(source.path,{bigint:true});
    if (key(before) !== key(after) || key(before) !== key(named) || BigInt(body.length) !== before.size ||
        realpathSync(source.path) !== source.resolved_path || createHash('sha256').update(body).digest('hex') !== source.sha256) throw fail();
  } finally { closeSync(fd); }
}

function verifyStartup(source: StartupFile): void {
  if (source.status === 'absent') return verifySource({path:source.path,kind:'absent'});
  const fd = openSync(source.path, constants.O_RDONLY | constants.O_NONBLOCK);
  try {
    const before = fstatSync(fd,{bigint:true});
    const identity = () => {
      const target = statSync(source.path,{bigint:true}), entry = lstatSync(source.path,{bigint:true}), resolved = realpathSync(source.path);
      if (key(target) !== key(fstatSync(fd,{bigint:true})) || resolved !== source.resolved_path) throw fail();
      return hash(Buffer.from(JSON.stringify([resolved,key(target),key(entry)])));
    };
    if (!before.isFile() || before.size !== BigInt(source.bytes!) || identity() !== source.identity_sha256 ||
        hash(readFileSync(fd)) !== source.sha256 || identity() !== source.identity_sha256) throw fail();
  } finally { closeSync(fd); }
}

function verifyInstalledBinding(config: SessionConfig, requireArtifact: boolean, adoptedPaths = new Set<string>()): void {
  if (!config.installed_settings) return;
  try {
    const binding = installedSettings(config.installed_settings);
    if (config.probe || fingerprint(binding.artifact) !== fingerprint(config.settings_artifact ?? null)) throw fail();
    const names = new Set((config.plugin_selections ?? []).map(p => p.name));
    if (binding.selections.some(p => names.has(p.name))) throw fail();
    for (const source of binding.source_files) verifySource(source);
    for (const source of binding.startup_files) if (!adoptedPaths.has(source.path)) verifyStartup(source);
    if (!requireArtifact) return;
    verifySource(binding.artifact.file);
    if (binding.artifact.original.kind === 'file') verifySource(binding.artifact.original);
    const value = record(JSON.parse(readFileSync(binding.artifact.file.path,'utf8')));
    const enabled = record(value.enabledPlugins);
    if (binding.selections.some(p => enabled[p.native_source!] !== (p.mode === 'enabled'))) throw fail();
  } catch { throw fail(); }
}

export function verifyInstalledSettings(config: SessionConfig, requireArtifact = true): void {
  verifyInstalledBinding(config, requireArtifact);
}

// A live settings guard keeps the old query's local version in force. Only
// those explicit local paths can differ while preparing the new version.
export function verifyInstalledSettingsAdoption(previous: SessionConfig, target: SessionConfig): void {
  const before = previous.installed_settings, after = target.installed_settings;
  const paths = new Set((previous.settings_sources ?? []).map(s => s.path));
  if (!before || !after || previous.settings_guard_version !== 1 || paths.size < 2 ||
      fingerprint([...paths]) !== fingerprint((target.settings_sources ?? []).map(s => s.path)) ||
      fingerprint(before.selections) !== fingerprint(after.selections) || fingerprint(before.source_files) !== fingerprint(after.source_files) ||
      fingerprint(before.artifact.original) !== fingerprint(after.artifact.original) || before.artifact.file.sha256 !== after.artifact.file.sha256 ||
      fingerprint(before.startup_files.filter(f => !paths.has(f.path))) !== fingerprint(after.startup_files.filter(f => !paths.has(f.path)))) throw fail();
  verifyInstalledSettings(target, false);
  verifyInstalledBinding(previous, true, paths);
}

// Restoring original local bytes creates a new observed identity. The caller
// must persist this exact target in its recovery operation before opening it.
export function installedSettingsRecovery(previous: SessionConfig, input: unknown, failedSources: SettingsSource[] | undefined, requireArtifact = false): SessionConfig {
  const before = previous.installed_settings, after = installedSettings(input);
  const original = previous.settings_sources;
  if (!before || !original || !failedSources || previous.settings_guard_version !== 1 ||
      fingerprint(original.map(s => s.path)) !== fingerprint(failedSources.map(s => s.path))) throw fail();
  const restored = new Set(original.filter((s, i) => fingerprint(s) !== fingerprint(failedSources[i])).map(s => s.path));
  const withoutRestoredIdentity = (files: StartupFile[]) => files.map(f => restored.has(f.path) && f.status === 'read' ? {...f, identity_sha256: ''} : f);
  if (!restored.size || before.startup_source_version === after.startup_source_version ||
      fingerprint(before.selections) !== fingerprint(after.selections) || fingerprint(before.source_files) !== fingerprint(after.source_files) ||
      fingerprint(before.artifact.original) !== fingerprint(after.artifact.original) || before.artifact.file.sha256 !== after.artifact.file.sha256 ||
      fingerprint(withoutRestoredIdentity(before.startup_files)) !== fingerprint(withoutRestoredIdentity(after.startup_files))) throw fail();
  verifySettingsSources(original);
  // The retained original artifact and every unaffected startup file still
  // match. Only the restored local files use the explicitly supplied version.
  verifyInstalledBinding(previous, true, restored);
  const target = {...previous, installed_settings: after, settings_artifact: after.artifact};
  verifyInstalledSettings(target, requireArtifact);
  verifyPluginSources(target);
  return target;
}
