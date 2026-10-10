export type ConfigurationFailure = {
  version: number; operation: string; baseline: string; stage: string;
  reason: string; cleanup: string; at: string; detail: string;
};

const stages: Record<string,string> = {
  open_original:'重开原查询', check_original_components:'核对原能力组件',
  close_original:'关闭原查询', check_original_history:'核对原历史', open_candidate:'启动候选配置',
  check_settings:'核对候选设置', check_candidate_history:'核对候选历史', check_components:'核对能力组件',
  check_idle:'核对空闲状态', check_sources:'复核来源文件', commit:'保存原生结果',
};

export function ConfigurationFailureDetails({failure,operation,baseline,connected,recovery=false,historical=false}: {
  failure?:ConfigurationFailure; operation?:string; baseline?:string; connected:boolean;recovery?:boolean;historical?:boolean;
}) {
  if (!failure || !operation || !baseline || failure.version!==1 || failure.operation!==operation || failure.baseline!==baseline ||
      !Object.hasOwn(stages,failure.stage) || !['confirmed','unconfirmed'].includes(failure.cleanup) ||
      !Number.isFinite(Date.parse(failure.at)) || (failure.reason!=='stage_failed' && !(failure.stage==='check_settings' && ['settings_parse_errors','policy_unreadable'].includes(failure.reason)))) return null;
  return <section aria-label={recovery?"原配置恢复失败记录":"配置应用失败记录"} className="snapshot-asset">
    <p className="error">{recovery?"原配置恢复失败":"配置应用失败"} · {recovery && failure.stage==="open_candidate"?"启动原配置":stages[failure.stage]}</p>
    <p>{historical?'失败时的记录：':''}{failure.detail}</p>
    <p className="hint">{failure.cleanup==='confirmed'?'本次自有查询已确认退出；已发生的副作用不会因此撤销。':'自有查询退出尚未确认；可能仍有执行或副作用。'}</p>
    {!connected?<p className="hint">连接已断开，以下为已保存记录；当前执行状态仍待核对。</p>:null}
    <small>记录时间：{new Date(failure.at).toLocaleString()}</small>
    <p className="mono">关联操作：{failure.operation}</p>
    <p className="hint">{historical?"此失败候选已归档，原配置恢复结果已核对；失败过程中发生的副作用保留。":"保留原快照与候选，继续暂停；不会自动回滚或重复应用。"}</p>
  </section>;
}
