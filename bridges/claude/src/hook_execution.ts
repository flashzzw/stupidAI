// Native hook events identify an invocation, not the plugin that supplied it.
// Retain its lifecycle and exit result without persisting command/output text.
export function sanitizeHookEvent<T extends { type: string; subtype?: string }>(event: T): T {
  if (event.type !== 'system' || !['hook_started', 'hook_progress', 'hook_response'].includes(event.subtype ?? '')) return event;
  return { ...event, hook_name: '', stdout: '', stderr: '', output: '' };
}

export function unfinishedHooks(entries: readonly { kind: string; event?: { type: string; subtype?: string; hook_id?: string; outcome?: string } }[]): string[] {
  const pending = new Set<string>(), started = new Set<string>();
  for (const { event } of entries) {
    if (event?.type !== 'system' || !event.hook_id) continue;
    if (event.subtype === 'hook_started') started.add(event.hook_id);
    if (event.subtype === 'hook_started' || event.subtype === 'hook_progress') pending.add(event.hook_id);
    if (event.subtype === 'hook_response') {
      if (started.has(event.hook_id) && ['success', 'error', 'cancelled'].includes(event.outcome ?? '')) pending.delete(event.hook_id);
      else pending.add(event.hook_id);
    }
  }
  return [...pending];
}
