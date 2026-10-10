import { readFileSync } from 'node:fs';
import { dirname, isAbsolute, resolve } from 'node:path';
import type { McpServerConfig, Query } from '@anthropic-ai/claude-agent-sdk';
import { MCPAuthenticationRequired, NotSent, record, text } from './protocol.js';

export type MCPSelection = { asset_id: string; server: string; mode: 'enabled' | 'disabled'; source_path: string };
export type MCPInventory = { available: boolean; at: string; servers: { name: string; status: string; source: string; tools: string[] }[] };
export const mcpToolPrefix = (server: string) => `mcp__${server.replace(/[^A-Za-z0-9_-]/g, '_')}__`;

export function mcpSelections(value: unknown): MCPSelection[] {
  if (!Array.isArray(value) || value.length > 16) throw new NotSent('invalid MCP selections');
  const seen = new Set<string>();
  return value.map(raw => {
    const item = record(raw), server = text(item.server, 'MCP server'), asset = text(item.asset_id, 'MCP asset');
    if (!/^[A-Za-z0-9_-]+$/.test(server) || !/^[A-Za-z0-9_-]+$/.test(asset) || seen.has(server) || !['enabled', 'disabled'].includes(String(item.mode))) throw new NotSent('invalid or duplicate MCP selection');
    seen.add(server);
    const source = text(item.source_path, 'MCP source');
    if (!isAbsolute(source)) throw new NotSent('MCP source must be absolute');
    return { asset_id: asset, server, mode: item.mode as MCPSelection['mode'], source_path: source };
  });
}
export function mcpDefinitions(selections: MCPSelection[]): Record<string, McpServerConfig> {
  const servers: Record<string, McpServerConfig> = Object.create(null);
  for (const selection of selections) {
    let source: Record<string, unknown>;
    try { source = record(JSON.parse(readFileSync(selection.source_path, 'utf8'))); }
    catch { throw new NotSent('MCP definition cannot be read'); }
    const server = record(record(source.mcpServers)[selection.server]);
    if (server.type === 'http' || server.type === 'sse') {
      if (Object.keys(server).some(key => !['type', 'url', 'headers', 'headersHelper'].includes(key))) throw new NotSent('unsupported remote MCP configuration field');
      if (typeof server.url !== 'string') throw new NotSent('remote MCP needs an HTTP/HTTPS URL');
      try {
        const endpoint = new URL(server.url);
        if (!['http:', 'https:'].includes(endpoint.protocol) || !endpoint.hostname) throw new Error();
      } catch { throw new NotSent('remote MCP needs an HTTP/HTTPS URL'); }
      const headers = server.headers === undefined ? {} : record(server.headers);
      if (Object.entries(headers).some(([key, value]) => !key || /[\r\n:]/.test(key) || typeof value !== 'string' || /[\r\n]/.test(value))) throw new NotSent('invalid MCP headers');
      const headersHelper = server.headersHelper;
      if (headersHelper !== undefined && (typeof headersHelper !== 'string' || !headersHelper.trim() || headersHelper.includes('\0'))) throw new NotSent('invalid MCP headersHelper command');
      // CLI 2.1.280 accepts this field even though SDK 0.3.280's public MCP
      // type omits it. Keep native shell/cwd semantics; never evaluate it here.
      if (selection.mode === 'enabled') servers[selection.server] = { type: server.type, url: server.url, headers: headers as Record<string, string>,
        ...(headersHelper === undefined ? {} : { headersHelper }) };
      continue;
    }
    if (Object.keys(server).some(key => !['type', 'command', 'args', 'env'].includes(key))) throw new NotSent('unsupported stdio MCP configuration field');
    if ((server.type !== undefined && server.type !== 'stdio') || typeof server.command !== 'string' || !server.command || server.url !== undefined) throw new NotSent('invalid stdio MCP definition or unsupported transport');
    if (server.args !== undefined && (!Array.isArray(server.args) || server.args.some(value => typeof value !== 'string'))) throw new NotSent('invalid MCP arguments');
    const env = server.env === undefined ? {} : record(server.env);
    if (Object.values(env).some(value => typeof value !== 'string')) throw new NotSent('invalid MCP environment references');
    // Only explicit file paths are rooted at the resource. The native subprocess
    // keeps the selected project cwd, and the native CLI expands ${ENV} values.
    const sourcePath = (value: string) => value.startsWith('./') || value.startsWith('../') || value.startsWith('scripts/') ? resolve(dirname(selection.source_path), value) : value;
    if (selection.mode === 'enabled') servers[selection.server] = { type: 'stdio', command: sourcePath(server.command), args: ((server.args ?? []) as string[]).map(sourcePath), env: env as Record<string, string> };
  }
  return servers;
}
export async function mcpInventory(run?: Query): Promise<MCPInventory> {
  const at = new Date().toISOString();
  if (!run) return { available: false, at, servers: [] };
  try {
    const statuses = await run.mcpServerStatus();
    return { available: true, at, servers: statuses.map(server => ({ name: server.name, status: server.status,
      source: server.source ?? '', tools: (server.tools ?? []).map(tool => tool.name).sort() })).sort((a, b) => a.name.localeCompare(b.name)) };
  } catch { return { available: false, at, servers: [] }; }
}
export function mcpMatches(selections: MCPSelection[], inventory: MCPInventory, previous: MCPSelection[] = []): boolean {
  if (!inventory.available) return false;
  // Released names may disappear or return to a native inherited source. A
  // lingering dynamic definition cannot prove that its override was removed.
  if (previous.some(old => !selections.some(s => s.server === old.server) && inventory.servers.some(s => s.name === old.server && (!s.source || s.source === 'dynamic')))) return false;
  return selections.every(selection => {
    const matches = inventory.servers.filter(server => server.name === selection.server);
    const server = matches[0];
    if (selection.mode === 'disabled') return matches.length === 0 || matches.length === 1 && server?.status === 'disabled' && server.tools.length === 0;
    return matches.length === 1 && server?.status === 'connected' && server.source === 'dynamic';
  });
}
function mismatchMessage(selections: MCPSelection[], inventory: MCPInventory): string {
  if (!inventory.available) return 'MCP 原生清单不可读取，无法确认本次选择；请核对原连接。';
  const sources = new Set(['user', 'project', 'local', 'dynamic', 'managed', 'enterprise', 'plugin', 'claudeai', 'agent', 'sdk']);
  return selections.flatMap(selection => {
    if (mcpMatches([selection], inventory)) return [];
    const matches = inventory.servers.filter(server => server.name === selection.server);
    const name = `MCP ${selection.server}`;
    if (matches.length > 1) return [`${name} 在原生清单中重复，无法唯一核对来源`];
    const server = matches[0];
    if (!server) return [`${name} 未出现在原生清单；请核对原生来源和 MCP 允许/拒绝策略`];
    const source = sources.has(server.source) ? server.source : '未知来源';
    if (selection.mode === 'disabled') return [`${name} 仍从 ${source} 来源加载，无法确认本次禁用`];
    if (server.source !== 'dynamic') return [`${name} 来自 ${source}，与本次动态选择不一致`];
    return [`${name} ${server.status === 'failed' ? '连接失败' : '连接尚未完成'}；请核对服务及认证配置`];
  }).join('；');
}
export async function verifyMCP(run: Query, selections: MCPSelection[]): Promise<void> {
  if (!selections.length) return;
  let inventory: MCPInventory = { available: false, at: '', servers: [] };
  for (let attempt = 0; attempt < 60; attempt++) {
    inventory = await mcpInventory(run);
    if (mcpMatches(selections, inventory)) return;
    const needsAuth = selections.filter(selection => selection.mode === 'enabled' && inventory.servers.some(server => server.name === selection.server && server.status === 'needs-auth'));
    if (needsAuth.length) throw new MCPAuthenticationRequired(`MCP ${needsAuth.map(s => s.server).join(', ')} 需要 OAuth 认证；请先在当前 Claude 执行配置中完成原生认证，再恢复连接。`);
    if (!inventory.servers.some(server => selections.some(selection => selection.mode === 'enabled' && selection.server === server.name) && server.status === 'pending')) break;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new NotSent(mismatchMessage(selections, inventory));
}
