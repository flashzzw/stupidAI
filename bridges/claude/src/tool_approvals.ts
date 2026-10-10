import type { HookCallbackMatcher, HookEvent } from '@anthropic-ai/claude-agent-sdk';
import { mcpToolPrefix } from './mcp.js';
import { executionPlugins } from './plugins.js';
import type { SessionConfig } from './protocol.js';

// Managed-only permission policy can ignore flagSettings.ask. SDK hooks ask
// before an allow rule can auto-approve, while native deny rules still win.
// Match only the tools for which this session already promises per-call review.
export function requiredToolApprovalHooks(config: SessionConfig): Partial<Record<HookEvent, HookCallbackMatcher[]>> {
  const tools = config.permission === 'read_only' ? ['Bash', 'Write', 'Edit', 'NotebookEdit'] : [];
  const prefixes = [
    ...(config.mcp_selections ?? []).filter(s => s.mode === 'enabled').map(s => `mcp__${s.server}__`),
    ...executionPlugins(config).filter(p => p.mode === 'enabled')
      .flatMap(p => p.components.filter(c => c.kind === 'mcp').map(c => mcpToolPrefix(c.native_name))),
  ];
  const escape = (value: string) => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const patterns = [...tools.map(escape), ...[...new Set(prefixes)].map(prefix => escape(prefix) + '.*')];
  if (!patterns.length) return {};
  return { PreToolUse: [{ matcher: '^(?:' + patterns.join('|') + ')$', hooks: [async () => ({
    hookSpecificOutput: { hookEventName: 'PreToolUse', permissionDecision: 'ask',
      permissionDecisionReason: '此工作台会话要求逐次批准该工具操作' },
  })] }] };
}
