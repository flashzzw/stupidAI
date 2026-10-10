import { useState } from 'react';
import type { SessionAccess } from './SessionAccessDetails';

type Summary = {
  id: string; title: string; project_id: string; status: string; archived: boolean; control_mode: string; activity_at?: string;
  access?: SessionAccess;
  scope: { cwd: string; instance: { id?: string; name?: string; kind: string }; route: { id: string; model: string } };
};

export function SessionList({ sessions, projects, selected, labels, select }: {
  sessions: Summary[]; projects: { id: string; name: string; cwd: string }[];
  selected: string; labels: Record<string, string>; select: (id: string) => void;
}) {
  const [search, setSearch] = useState('');
  const [agent, setAgent] = useState('');
  const [status, setStatus] = useState('');
  const [scope, setScope] = useState('active');
  const [source, setSource] = useState('');
  const visible = sessions.filter((session) => {
    const project = projects.find((p) => p.id === session.project_id);
    return (scope === 'all' || session.archived === (scope === 'archived')) &&
      (!agent || session.scope.instance.kind === agent) && (!status || session.status === status) && (!source || (session.access?.source ?? 'unknown') === source) &&
      `${session.title} ${session.scope.cwd} ${project?.name ?? ''} ${session.scope.instance.name ?? ''} ${session.scope.instance.id ?? ''} ${session.scope.route.id} ${session.scope.route.model} ${session.access?.source_label ?? ''} ${session.access?.context?.binding.profile ?? ''} ${session.access?.context?.binding.state_namespace ?? ''} ${session.access?.context?.executor?.user ?? ''} ${session.access?.context?.executor?.wsl_distro ?? ''}`.toLowerCase().includes(search.toLowerCase());
  });
  const groups = [...projects, ...[...new Set(visible.map((s) => s.project_id))].filter((id) => !projects.some((p) => p.id === id)).map((id) => ({ id, name: id || '未登记项目', cwd: '' }))];
  return <>
    <label className="search"><span className="sr">搜索会话</span><input placeholder="搜索会话或项目…" value={search} onChange={(e) => setSearch(e.target.value)} /></label>
    <div className="session-filters">
      <label><span className="sr">Agent 筛选</span><select value={agent} onChange={(e) => setAgent(e.target.value)}><option value="">所有 Agent</option>{['codex', 'claude', 'opencode'].map((kind) => <option key={kind}>{kind}</option>)}</select></label>
      <label><span className="sr">状态筛选</span><select value={status} onChange={(e) => setStatus(e.target.value)}><option value="">所有状态</option>{['idle', 'running', 'waiting_approval', 'waiting_input', 'unknown'].map((state) => <option key={state} value={state}>{labels[state]}</option>)}</select></label>
      <label className="session-scope"><span className="sr">会话范围</span><select value={scope} onChange={(e) => setScope(e.target.value)}><option value="active">未归档</option><option value="archived">已归档</option><option value="all">全部会话</option></select></label>
      <label className="session-scope"><span className="sr">接续来源</span><select value={source} onChange={(e) => setSource(e.target.value)}><option value="">所有接续来源</option><option value="platform">工作台创建</option><option value="observation">外部事件记录</option><option value="native">外部原生只读绑定</option><option value="probe">隔离路由探测</option><option value="unknown">来源待核对</option></select></label>
    </div>
    <div className="session-list">{groups.map((project) => {
      const items = visible.filter((session) => session.project_id === project.id);
      return items.length ? <section className="project-group" key={project.id} aria-label={project.name || project.id}>
        <h3>{project.name || project.id}<span>{items.length}</span></h3>
        {items.map((session) => <button key={session.id} className={`session-row ${selected === session.id ? 'selected' : ''}`} onClick={() => select(session.id)}>
          <span className="session-title">{session.title}</span><span className="session-meta"><span>{session.scope.instance.kind}</span><span className={session.status === 'unknown' ? 'warning' : ''}>{labels[session.status] ?? session.status}</span></span>
          <small>{session.scope.instance.name || session.scope.instance.id || '未登记执行配置'} · {session.access?.source_label ?? '来源待核对'}</small>
          {session.access?.context?.registry_status === 'changed' || session.access?.context?.registry_status === 'missing' ? <small className="warning">绑定与当前登记需核对</small> : null}
          {session.access?.context?.executor ? <small>后台采样：{session.access.context.executor.wsl_distro || session.access.context.executor.os} · UID {session.access.context.executor.uid}</small> : null}
          <small>{session.access?.source === 'native' ? '只读历史' : session.control_mode === 'observed' ? '仅观察' : session.archived ? '已归档' : session.scope.route.id}{session.activity_at ? <time dateTime={session.activity_at} title={new Date(session.activity_at).toLocaleString()}>{new Date(session.activity_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</time> : null}</small>
        </button>)}
      </section> : null;
    })}{visible.length === 0 ? <p className="empty-list">{sessions.length ? '没有符合筛选条件的会话。' : '还没有会话。选择项目和路由，开始第一轮对话。'}</p> : null}</div>
  </>;
}
