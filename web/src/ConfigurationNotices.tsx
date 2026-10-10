export type ConfigurationNoticeHistory = {
  source: string; available: boolean; complete: boolean; registered: boolean; recording_failed: boolean;
  truncated: boolean; current_query_id?: string; current_count: number;
  items: {id: string; native_id: string; cwd: string; native_cwd: string; query_id: string; configuration_id: string;
    at: string; source: string; path: string; prompt_id?: string; agent_id?: string; agent_type?: string; current: boolean;
    receipt_operation?: string; receipt_correlation?: string; receipt_snapshot_id?: string}[];
};

const sources: Record<string, string> = {user_settings: '用户设置', project_settings: '项目设置', local_settings: '项目本地设置',
  policy_settings: '管理员受管设置', skills: 'Skill 来源'};
const visible = (text: string) => text.replace(/\\/g, '\\\\').replace(/[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/gu, value =>
  `\\u{${value.codePointAt(0)!.toString(16).toUpperCase()}}`);
const sourceName = (source: string) => Object.hasOwn(sources, source) ? sources[source] : `未知来源（${visible(source)}）`;

export function ConfigurationNotices({history, live}: {history?: ConfigurationNoticeHistory; live: boolean}) {
  return <details className="instruction-loads configuration-notices"><summary>原生配置变化通知{history?.available ? ` · ${history.items.length} 条已保存` : ''}</summary>
    <p className="hint">原生 CLI 报告配置来源变化时，工作台记录通知。收到通知后，接收时仍在等待的旧工具请求不能继续批准；可明确拒绝或中止原输入。</p>
    {live && history?.current_query_id ? <p role="status">{history.registered ? '原生已确认当前查询的通知回调登记。' : '当前查询的通知回调登记尚未确认。'} 已保存当前查询通知 {history.current_count} 条。</p>
      : <p className="hint">当前原生查询尚未核对；以下保留已保存通知。</p>}
    {history?.recording_failed ? <p className="error" role="status">部分配置通知未能保存或核对；以下只展示有效记录。</p> : null}
    {!history?.available ? <p className="hint">尚无可核对的配置通知记录。</p> : <>
      {!history.items.length ? <p className="hint">目前没有已收到的配置通知；空记录不表示配置未变化。</p> : null}
      {history.truncated ? <p className="hint">当前展示最近 128 条有效通知，更早的日记记录仍保留。</p> : null}
      {history.items.map(notice => <div key={notice.id} className="snapshot-asset">
        <p>{sourceName(notice.source)} · {live && history.current_query_id ? notice.current ? '当前查询' : '先前查询' : '已保存记录'}</p>
        {notice.path ? <p className="mono">{visible(notice.path)}</p> : <p className="hint">原生通知未报告文件路径。</p>}
        <small>收到时间：{new Date(notice.at).toLocaleString()}</small>
        {notice.native_cwd !== notice.cwd ? <p className="mono">通知时的工作目录：{visible(notice.native_cwd)}</p> : null}
        {notice.agent_id ? <p>原生子代理：{visible(notice.agent_id)}{notice.agent_type ? ` · ${visible(notice.agent_type)}` : ''}</p> : null}
        {notice.receipt_operation && notice.receipt_correlation === 'journal_in_flight' ? <>
          <p className="mono">接收时的在途输入：{notice.receipt_operation}</p>
          {notice.receipt_snapshot_id ? <small className="mono">该输入的原快照：{notice.receipt_snapshot_id}</small> : null}
          <p className="hint">依据同一查询的日记接收顺序关联，不证明该输入受到配置影响。</p>
        </> : <p className="hint">未关联通知接收时的工作台输入。</p>}
        <details><summary>通知归属</summary>
          <p className="mono">通知 ID：{notice.id}</p>
          <p className="mono">工作台查询标识：{notice.query_id}</p>
          {notice.prompt_id ? <p className="mono">原生提示 ID：{notice.prompt_id}</p> : <p className="hint">本通知没有原生提示 ID。</p>}
          <p className="mono">查询启动配置标识：{notice.configuration_id}</p>
          <p className="hint">原生提示 ID 独立保留；查询启动配置标识不代表通知文件的实际加载版本。</p>
        </details>
      </div>)}
    </>}
    <p className="hint">仅覆盖成功收到并保存的通知，不读取文件正文；不证明权限规则已变化、配置已应用或已被阻止，也不保证全部变化均已收到。</p>
  </details>;
}
