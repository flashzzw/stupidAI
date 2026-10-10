export type HookExecution = {
  id: string; native_run_id?: string; turn_id?: string; source_path?: string;
  asset_id?: string; asset_version?: string; source_hash?: string; execution_snapshot_id?: string;
  event: string; operation?: string; status: string; native_status?: string;
  failure_kind?: string; duration_ms?: number; source: string;
  started_at?: string; finished_at?: string; exit_code?: number; detail: string;
};

const statuses: Record<string, string> = { running: '执行中', success: '成功结束', error: '失败结束', cancelled: '已取消', unknown: '终态待核对' };
const sources: Record<string, string> = { plugin: 'CLI 插件', user: '用户', project: '项目', system: '系统', mdm: '设备管理' };

export function HookExecutions({ executions, backend, connected }: {
  executions?: HookExecution[]; backend: 'codex' | 'claude'; connected: boolean;
}) {
  if (!executions?.length) return null;
  return <details className="hook-executions"><summary>原生 hook 执行 · {executions.length} 项</summary>
    <p className="hint">{backend === 'codex'
      ? '这些记录来自收到的同步原生执行事件，异步 hook 不在此通知的覆盖范围内。事件报告来源路径，但没有完整插件 ID；来源清单中的发现状态另行核对。'
      : '这些记录来自原生执行事件。来源未报告，不能据事件名称指定某个插件；来源清单中的登记状态另行核对。'}</p>
    {executions.map(hook => <div key={hook.id} className="snapshot-asset">
      <p>{hook.event} · {statuses[!connected && hook.status === 'running' ? 'unknown' : hook.status] ?? '终态待核对'}</p>
      <p className="mono">执行 ID：{hook.native_run_id || hook.id}</p>
      {hook.turn_id ? <p className="mono">原生轮次：{hook.turn_id}</p> : null}
      <p>{hook.operation ? `关联操作：${hook.operation}` : hook.turn_id ? '尚未关联工作台操作' : '未关联模型轮次'}</p>
      {hook.asset_id ? <p>原操作绑定：{hook.asset_id} · 资源 {hook.asset_version}<small className="mono">快照 {hook.execution_snapshot_id}</small><small className="mono">完整内容标识 {hook.source_hash}</small></p> : null}
      {hook.source_path ? <><p>原生来源：{sources[hook.source] ?? hook.source}</p><p className="mono">{hook.source_path}</p></> : null}
      {hook.native_status ? <p>原生状态：{hook.native_status}</p> : null}
      {hook.failure_kind === 'timeout' ? <p className="error">原生报告：hook 超时</p> : null}
      {hook.duration_ms !== undefined ? <p>原生耗时：{hook.duration_ms} 毫秒</p> : null}
      {hook.exit_code !== undefined ? <p>原生退出码：{hook.exit_code}</p> : null}
      <small>{hook.started_at ? `开始：${new Date(hook.started_at).toLocaleString()}` : '缺少开始事件'}</small>
      {hook.finished_at ? <small>结束：{new Date(hook.finished_at).toLocaleString()}</small> : null}
      <p className="hint">{hook.detail}</p>
    </div>)}
    {!connected ? <p className="hint">连接尚未核对；保留已记录结果，未结束的调用需重新确认。</p> : null}
    <p className="hint">已记录的 hook 若仍在执行或缺少完整终态，会保留占用。失败或取消记录保留其原生结果；是否阻止后续任务由原生规则决定。</p>
  </details>;
}
