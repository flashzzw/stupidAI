import type { HookCallback, HookEvent, HookCallbackMatcher } from '@anthropic-ai/claude-agent-sdk';

export type SettingsChange = { source: string; path: string; at: string; decision: 'block_requested' };

// Returning the native blocking decision must not depend on diagnostic I/O.
// Native managed policy and other source types keep their own semantics.
export function localSettingsHooks(id: string, record: (change: SettingsChange) => void, failed: () => void): Partial<Record<HookEvent, HookCallbackMatcher[]>> {
  const callback: HookCallback = async input => {
    try {
      const source = input.hook_event_name === 'ConfigChange' ? input.source : 'unknown';
      const path = input.hook_event_name === 'ConfigChange' && typeof input.file_path === 'string' && input.file_path.length <= 16_384 ? input.file_path : '';
      record({source: input.session_id === id ? source : 'unknown', path, at: new Date().toISOString(), decision:'block_requested'});
    } catch { try { failed(); } catch { /* Diagnostic failure must not allow native auto-apply. */ } }
    return {decision:'block'};
  };
  return {ConfigChange:[{matcher:'project_settings|local_settings',hooks:[callback]}]};
}

export function settingsGuardEvidence(version: number | undefined, live: boolean, policy: {
  all_disabled: unknown; managed_only: unknown; disabled_by_policy: unknown; plugin_only: unknown;
  policy_unreadable: boolean; safe_mode: boolean; bare_mode: boolean; settings_error_count: number;
} | undefined, changes: SettingsChange[], recordFailed: boolean) {
  if (!version) return undefined;
  const unavailable = !live || !policy || policy.all_disabled || policy.managed_only || policy.disabled_by_policy || policy.plugin_only || policy.policy_unreadable || policy.safe_mode || policy.bare_mode || policy.settings_error_count > 0 || recordFailed;
  return {version, state:unavailable ? 'unavailable' : 'registered', changes:changes.slice(-16),
    detail: recordFailed ? '设置变化记录写入失败；阻止决策仍已返回，后续输入暂停。' : unavailable ? '当前连接或原生 hook 策略无法确认此机制可用；不能据此声称阻止了自动应用。' : '已为项目/本地设置注册阻止自动应用的回调；文件版本检查仍独立执行。回调记录表示已返回阻止决策，不是全部配置冻结证明。'};
}
