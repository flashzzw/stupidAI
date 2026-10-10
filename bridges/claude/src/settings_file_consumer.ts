import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process';
import { closeSync, constants, fstatSync, openSync } from 'node:fs';
import { basename, dirname, isAbsolute, normalize } from 'node:path';
import type { SpawnOptions } from '@anthropic-ai/claude-agent-sdk';
import { NotSent, record, text, uuid } from './protocol.js';

export type FileSource = { path: string; resolved_path: string; kind: 'file'; mode: number; sha256: string };
export type SettingsFileArtifact = { id: string; original: FileSource | { kind: 'absent' }; file: FileSource };

const failure = () => new NotSent('执行版本的派生设置使用绑定无效');
export const fileSource = (input: unknown): FileSource => {
  const value = record(input);
  const path = text(value.path, 'settings path'), resolved = text(value.resolved_path, 'resolved settings path');
  if (value.kind !== 'file' || !isAbsolute(path) || normalize(path) !== path || !isAbsolute(resolved) || normalize(resolved) !== resolved ||
      !Number.isInteger(value.mode) || Number(value.mode) < 0 || Number(value.mode) > 0o7777 || typeof value.sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(value.sha256)) throw failure();
  return { path, resolved_path: resolved, kind: 'file', mode: Number(value.mode), sha256: value.sha256 };
};

export function settingsFileArtifact(input: unknown): SettingsFileArtifact {
  const value = record(input), id = uuid(value.id), file = fileSource(value.file), original = record(value.original);
  if (file.mode !== 0o600 || basename(file.path) !== `.netcap-settings-${id}.json` || basename(file.resolved_path) !== basename(file.path)) throw failure();
  return { id, file, original: original.kind === 'absent' ? {kind:'absent'} : fileSource(original) };
}

// The Go exec entry takes the shared lock and revalidates the named file after
// the hand-off, then execs the CLI with fd 3 preserved. SDK pipes and PID keep
// their normal meaning; closing either parent cannot release a live consumer.
export function spawnSettingsConsumer(options: SpawnOptions, config: { cwd: string; settings_file?: string; settings_artifact?: SettingsFileArtifact }): ChildProcessWithoutNullStreams {
  const inherited = {cwd:options.cwd,env:options.env,signal:options.signal};
  if (!config.settings_artifact) return spawn(options.command, options.args, {...inherited,stdio:['pipe','pipe','pipe']});
  const artifact = settingsFileArtifact(config.settings_artifact);
  if (dirname(artifact.file.path) !== (config.settings_file ? dirname(config.settings_file) : config.cwd) ||
      (config.settings_file ? artifact.original.kind !== 'file' || artifact.original.path !== config.settings_file : artifact.original.kind !== 'absent')) throw failure();
  const executable = process.env.NETCAP_CLAUDE_SETTINGS_EXEC;
  if (!executable || !isAbsolute(executable)) throw new NotSent('执行版本的设置使用入口不可用');
  const fd = openSync(artifact.file.path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try {
    const info = fstatSync(fd);
    if (!info.isFile() || (info.mode & 0o777) !== 0o600 || info.uid !== process.getuid?.()) throw failure();
    // The explicit first three pipes satisfy the SDK's process interface.
    return spawn(executable, ['__claude-settings-exec-v1', JSON.stringify(artifact.file), options.command, ...options.args],
      {...inherited,stdio:['pipe','pipe','pipe',fd]}) as ChildProcessWithoutNullStreams;
  } finally { closeSync(fd); }
}
