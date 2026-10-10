export type InstructionLoadHistory = {
  source: string; available: boolean; complete: boolean; recording_failed: boolean; truncated: boolean;
  current_query_id?: string;
  items: {id: string; native_id: string; cwd: string; native_cwd: string; query_id: string; configuration_id: string;
    at: string; path: string; source: string; reason: string; current: boolean;
    prompt_id?: string; agent_id?: string; agent_type?: string; parent_path?: string; trigger_path?: string; globs?: string[];
    operation?: string; input_correlation?: string; execution_snapshot_id?: string}[];
};

const reasons: Record<string, string> = {session_start: '查询启动', nested_traversal: '访问子目录', path_glob_match: '路径规则匹配', include: '指令文件导入', compact: '上下文压缩'};
const sources: Record<string, string> = {User: '用户', Project: '项目（含上层目录）', Local: '项目本地', Managed: '管理员受管'};

export function InstructionLoads({history, live}: {history?: InstructionLoadHistory; live: boolean}) {
  return <details className="instruction-loads"><summary>原生指令加载记录{history?.available ? ` · ${history.items.length} 项` : ''}</summary>
    <p className="hint">这些记录来自已收到的原生加载通知，可以补充汇总遗漏的导入或嵌套来源。这里只保存路径和通知元数据，不读取正文，也不证明内容版本、实际遵循或独立排除。</p>
    {history?.recording_failed ? <p className="error" role="status">部分加载通知未能保存或核对；以下只展示有效记录。</p> : null}
    {!history?.available ? <p className="hint">尚无可核对的加载通知记录；这不表示没有加载指令。</p> : <>
      {history.truncated ? <p className="hint">当前展示最近 128 条有效通知，更早的日记记录仍保留。</p> : null}
      {!history.items.length ? <p className="hint">目前没有已收到的加载通知；空记录不能证明未加载指令。</p> : null}
      {history.items.map(load => <div key={load.id} className="snapshot-asset">
        <p>{reasons[load.reason] ?? load.reason} · {live && history.current_query_id ? load.current ? '当前查询' : '先前查询' : '已保存记录'}</p>
        <p className="mono">{load.path}</p>
        <small>来源：{sources[load.source] ?? load.source} · 收到时间：{new Date(load.at).toLocaleString()}</small>
        {load.parent_path ? <p className="mono">导入文件：{load.parent_path}</p> : null}
        {load.trigger_path ? <p className="mono">触发文件：{load.trigger_path}</p> : null}
        {load.globs?.length ? <p className="mono">匹配规则：{load.globs.join('、')}</p> : null}
        {load.native_cwd !== load.cwd ? <p className="mono">通知时的工作目录：{load.native_cwd}</p> : null}
        {load.agent_id ? <p>原生子代理：{load.agent_id}{load.agent_type ? ` · ${load.agent_type}` : ''}</p> : null}
        {load.operation ? <><p className="mono">关联原操作：{load.operation}</p>
          {load.execution_snapshot_id ? <small className="mono">原操作的快照：{load.execution_snapshot_id}</small> : null}</>
          : load.prompt_id ? <p className="hint">原生提示尚未关联工作台输入操作。</p> : <p className="hint">本通知没有原生提示 ID，不指定输入操作。</p>}
        <details><summary>通知归属</summary>
          <p className="mono">工作台查询标识：{load.query_id}</p>
          {load.prompt_id ? <p className="mono">原生提示 ID：{load.prompt_id}</p> : null}
          {load.input_correlation === 'native_tool_message' ? <p className="hint">通过同一查询的原生工具事件核对输入 UUID。</p> : null}
          <p className="mono">查询配置标识：{load.configuration_id}</p>
          <p className="hint">此标识关联查询启动配置，不是已加载指令文件的版本。</p>
        </details>
      </div>)}
    </>}
    {!live ? <p className="hint">当前原生连接尚未核对；保留已保存通知，不把历史记录当作当前加载。</p> : null}
    <p className="hint">只覆盖成功收到并保存的通知，不保证全部历史或完整来源，也不授予执行权限。</p>
  </details>;
}
