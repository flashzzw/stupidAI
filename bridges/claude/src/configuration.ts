import { createHash, randomUUID } from 'node:crypto';
import { closeSync, fsyncSync, openSync, readFileSync, renameSync, unlinkSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import type { MCPInventory, MCPSelection } from './mcp.js';
import type { SkillInventory, SkillSelection } from './skills.js';
import type { SessionConfig } from './protocol.js';
import type { PluginInventory, PluginSelection } from './plugins.js';
import type { SettingsSource } from './settings_sources.js';
import type { InstalledSettings } from './installed_settings.js';

export const configurationStages = ['open_original','check_original_components','close_original','check_original_history','open_candidate','check_settings','check_candidate_history','check_components','check_idle','check_sources','commit'] as const;
export type ConfigurationFailure = {
  version: 1; operation: string; baseline: string; at: string;
  stage: typeof configurationStages[number];
  reason: 'stage_failed' | 'settings_parse_errors' | 'policy_unreadable';
  cleanup: 'unconfirmed' | 'confirmed';
};

// Only bounded identifiers and fixed reason codes may leave the native journal.
// Error messages, settings bodies, paths and tool output are never copied.
export function configurationFailure(value: unknown): ConfigurationFailure | undefined {
  if (!value || typeof value !== 'object') return;
  const v=value as Record<string,unknown>;
  if (v.version!==1 || typeof v.operation!=='string' || !/^[A-Za-z0-9_-]{1,200}$/.test(v.operation) ||
      typeof v.baseline!=='string' || !/^[a-f0-9]{64}$/.test(v.baseline) || typeof v.at!=='string' || v.at.length>64 || !Number.isFinite(Date.parse(v.at)) ||
      !configurationStages.includes(v.stage as ConfigurationFailure['stage']) || !['stage_failed','settings_parse_errors','policy_unreadable'].includes(String(v.reason)) ||
      v.reason!=='stage_failed' && v.stage!=='check_settings' || !['confirmed','unconfirmed'].includes(String(v.cleanup))) return;
  return {version:1,operation:v.operation,baseline:v.baseline,at:v.at,stage:v.stage as ConfigurationFailure['stage'],reason:v.reason as ConfigurationFailure['reason'],cleanup:v.cleanup as ConfigurationFailure['cleanup']};
}

export type ConfigurationRecovery = {
  operation: string; status: 'recovering' | 'verified'; at: string;
  history_digest?: string; inventory?: MCPInventory; skills?: SkillInventory; plugins?: PluginInventory;
  installed_settings?: InstalledSettings;
  original_query_retry?: OriginalQueryRecovery;
};
export type OriginalQueryRecovery = {version:1; failure:ConfigurationFailure};
export type ConfigurationChange = {
  operation: string; baseline: string; target: MCPSelection[]; previous: MCPSelection[];
  status: 'applying' | 'verified' | 'not_sent' | 'recovered'; at: string;
  recovery?: ConfigurationRecovery;
  history_digest?: string; inventory?: MCPInventory;
  skill_target?: SkillSelection[]; skill_previous?: SkillSelection[]; skills?: SkillInventory;
  plugin_target?: PluginSelection[]; plugin_previous?: PluginSelection[]; plugins?: PluginInventory;
  settings_target?: SettingsSource[]; settings_previous?: SettingsSource[];
  installed_target?: InstalledSettings; installed_previous?: InstalledSettings;
  original_plugins?: PluginInventory;
  progress?: {version:1; phase:'original' | 'candidate'};
};

// A complete original inventory cannot exist when its query failed before
// initialization. The durable phase proves that the candidate was not opened;
// this permits an explicit original-query retry, never a candidate rollback.
export function originalQueryRecoveryMatches(change: ConfigurationChange | undefined, input: unknown): input is OriginalQueryRecovery {
  if (!change || !['applying','recovered'].includes(change.status) || change.progress?.version !== 1 || change.progress.phase !== 'original' ||
      change.original_plugins !== undefined || !change.installed_target && !change.installed_previous || !input || typeof input !== 'object') return false;
  const proof = input as Record<string,unknown>, failure = configurationFailure(proof.failure);
  return proof.version === 1 && !!failure && failure.operation === change.operation && failure.baseline === change.baseline &&
    failure.cleanup === 'confirmed' && ['open_original','check_original_components'].includes(failure.stage);
}

export function originalQueryRecovery(change: ConfigurationChange | undefined, failure: unknown): OriginalQueryRecovery | undefined {
  const parsed = configurationFailure(failure);
  const proof = {version:1, failure:parsed};
  if (change?.status === 'applying' && !change.recovery && originalQueryRecoveryMatches(change, proof)) return proof;
}
export function fingerprint(value: unknown): string {
  const canonical = (item: unknown): unknown => Array.isArray(item) ? item.map(canonical) : item && typeof item === 'object'
    ? Object.fromEntries(Object.entries(item).sort(([a], [b]) => a.localeCompare(b)).map(([key, value]) => [key, canonical(value)])) : item;
  return createHash('sha256').update(JSON.stringify(canonical(value))).digest('hex');
}
export function readMetadata(path: string): { config: SessionConfig; change?: ConfigurationChange } {
  const { _configuration, ...config } = JSON.parse(readFileSync(path, 'utf8'));
  return { config, change: _configuration };
}
// Commit the active binding and verified outcome together. A crash before this
// rename leaves an applying record, never a fabricated successful version.
export function writeMetadata(path: string, config: SessionConfig, change: ConfigurationChange): void {
  const temporary = path + '.' + randomUUID() + '.tmp';
  const fd = openSync(temporary, 'wx', 0o600);
  try { writeFileSync(fd, JSON.stringify({ ...config, _configuration: change })); fsyncSync(fd); }
  finally { closeSync(fd); }
  try { renameSync(temporary, path); } catch (error) { unlinkSync(temporary); throw error; }
  const dir = openSync(dirname(path), 'r');
  try { fsyncSync(dir); } finally { closeSync(dir); }
}
