import { createHash } from 'node:crypto';
import type { permissionRuleInventory } from './permission_rules.js';

type Inventory = ReturnType<typeof permissionRuleInventory>;
export type PermissionSample = { at: string; hash: string; usable: boolean };
export type PermissionApproval = {
  version: 1; complete: false; query_id: string; configuration_id: string;
  baseline_at: string; baseline_hash: string; checked_at: string; checked_hash: string;
  state: 'observed' | 'changed' | 'unavailable';
  configuration_notice_id?: string;
};

// Compare observations, not permission semantics. Keep duplicate rows, unknown
// sources and ignored markers; neither a matching hash nor this guard grants
// permission or proves that the CLI has atomically adopted external policy.
export function permissionSample(inventory: Inventory): PermissionSample {
  const markers = new Map<string, number>();
  for (const row of inventory.items) {
    const key = JSON.stringify([row.behavior, row.source, row.rule]);
    markers.set(key, (markers.get(key) ?? 0) | (row.not_in_effect ? 2 : 1));
  }
  const rows = inventory.items.map(r => JSON.stringify([r.behavior, r.source, r.rule, r.editability, r.not_in_effect])).sort();
  const directories = inventory.directories.map(r => JSON.stringify([r.path, r.source])).sort();
  const errors = inventory.errors.map(r => JSON.stringify([r.file, r.field])).sort();
  return {at: inventory.at, hash: createHash('sha256').update('permission_approval_observation_v1\0').update(
    JSON.stringify([inventory.cwd, inventory.managed_only, rows, directories, errors])).digest('hex'),
    usable: errors.length === 0 && ![...markers.values()].includes(3)};
}

export function permissionApproval(query: string, configuration: string, sample?: PermissionSample): PermissionApproval {
  return {version: 1, complete: false, query_id: query, configuration_id: configuration,
    baseline_at: sample?.at ?? '', baseline_hash: sample?.hash ?? '', checked_at: sample?.at ?? '', checked_hash: sample?.hash ?? '',
    state: sample?.usable ? 'observed' : 'unavailable'};
}

export function observePermissionApproval(guard: PermissionApproval, sample?: PermissionSample): PermissionApproval {
  let state = guard.state;
  if (state === 'observed' && !sample?.usable) state = 'unavailable';
  if (state !== 'changed' && sample?.usable && guard.baseline_hash && sample.hash !== guard.baseline_hash) state = 'changed';
  // Once an original input/request loses its observation, later restoration
  // cannot turn it into a new approval request.
  return {...guard, state, checked_at: sample?.at ?? new Date().toISOString(), checked_hash: sample?.hash ?? ''};
}

export function configurationNoticeApproval(guard: PermissionApproval, noticeID?: string): PermissionApproval {
  return {...guard, state: guard.state === 'changed' ? 'changed' : 'unavailable',
    ...(!guard.configuration_notice_id && noticeID ? {configuration_notice_id: noticeID} : {})};
}

export function permissionApprovalFailure(guard?: PermissionApproval): string {
  return guard?.state === 'changed'
    ? '原输入等待期间已观察到权限规则变化，不能批准这条旧请求；请拒绝本次工具操作或中止原输入'
    : guard?.configuration_notice_id ? '原输入等待期间已收到配置变化通知，旧审批依据失效；请拒绝本次工具操作或中止原输入'
    : '原输入的权限规则观察不完整，不能批准这条请求；请拒绝本次工具操作或中止原输入';
}
