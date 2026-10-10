import { pluginSelections, type PluginSelection } from './plugins.js';
import { settingsSources, type SettingsSource } from './settings_sources.js';
import { settingsFileArtifact, type SettingsFileArtifact } from './settings_file_consumer.js';
import { installedSettings, type InstalledSettings } from './installed_settings.js';
import { mcpSelections, type MCPSelection } from './mcp.js';
import { skillSelections, type SkillSelection } from './skills.js';
export const protocolVersion = 1;

export class NotSent extends Error {}
export class MCPAuthenticationRequired extends NotSent {}

export function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new NotSent('expected an object');
  return value as Record<string, unknown>;
}

export function text(value: unknown, name: string): string {
  if (typeof value !== 'string' || !value || value.length > 200_000) throw new NotSent(`invalid ${name}`);
  return value;
}

export function uuid(value: unknown): string {
  const id = text(value, 'UUID');
  if (!/^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i.test(id)) throw new NotSent('invalid UUID');
  return id;
}

export type SessionConfig = {
  id: string;
  source: string;
  cwd: string;
  executable: string;
  model: string;
  endpoint: string;
  permission: 'read_only' | 'workspace';
  probe: boolean;
  settings_file?: string;
  settings_artifact?: SettingsFileArtifact;
  installed_settings?: InstalledSettings;
  settings_guard_version?: number;
  settings_sources?: SettingsSource[];
  mcp_selections?: MCPSelection[];
  skill_selections?: SkillSelection[];
  plugin_selections?: PluginSelection[];
};

export function sessionConfig(params: Record<string, unknown>): SessionConfig {
  if (params.settings_guard_version !== undefined && (params.settings_guard_version !== 1 || params.probe === true)) throw new NotSent('invalid local settings guard version');
  if (params.settings_sources !== undefined && params.settings_guard_version !== 1) throw new NotSent('local settings versions require a guard binding');
  if (params.probe === true && (params.settings_file !== undefined || params.settings_artifact !== undefined || params.installed_settings !== undefined || params.mcp_selections !== undefined || params.skill_selections !== undefined || params.plugin_selections !== undefined)) throw new NotSent('isolated probe cannot load work resources');
  const permission = params.permission;
  if (permission !== 'read_only' && permission !== 'workspace') throw new NotSent('invalid permission');
  return { id: uuid(params.native_id), source: uuid(params.source), cwd: text(params.cwd, 'cwd'),
    executable: text(params.executable, 'executable'), model: text(params.model, 'model'),
    endpoint: text(params.endpoint, 'endpoint'), permission, probe: params.probe === true,
    ...(params.settings_file === undefined ? {} : {settings_file: text(params.settings_file, 'settings file')}),
    ...(params.settings_artifact === undefined ? {} : {settings_artifact: settingsFileArtifact(params.settings_artifact)}),
    ...(params.installed_settings === undefined ? {} : {installed_settings: installedSettings(params.installed_settings)}),
    ...(params.settings_guard_version === undefined ? {} : {settings_guard_version: 1}),
    ...(params.settings_sources === undefined ? {} : {settings_sources: settingsSources(params.settings_sources)}),
    ...(params.mcp_selections === undefined ? {} : {mcp_selections: mcpSelections(params.mcp_selections)}),
    ...(params.skill_selections === undefined ? {} : {skill_selections: skillSelections(params.skill_selections)}),
    ...(params.plugin_selections === undefined ? {} : {plugin_selections: pluginSelections(params.plugin_selections)}) };
}
