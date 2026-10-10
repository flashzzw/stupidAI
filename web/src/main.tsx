import type { ConfigurationFailure } from './ConfigurationFailureDetails';
import type { SettingsGuard } from './ClaudeSettingsGuard';
import type { HookExecution } from './HookExecutions';
import type { PluginEvidence } from './ClaudePluginSelection';
import { StrictMode, useEffect, useLayoutEffect, useRef, useState, type FormEvent } from 'react';
import { createRoot } from 'react-dom/client';
import './style.css';
import { PendingPanel, type PendingAction, type CommandFields } from './PendingAction';
import { SessionList } from './SessionList';
import { SkillInputPicker, type SkillInputChoice } from './SkillInputPicker';
import { NativeSessionBrowser } from './NativeSessionBrowser';
import { NativeHistory } from './NativeHistory';
import { NativeObservation } from './NativeObservation';
import { SessionAccessDetails, type SessionAccess } from './SessionAccessDetails';
import { EventTimeline, type DomainEvent } from './EventTimeline';
import { InstanceDetails, supports, type InstanceStatus } from './InstanceDetails';
import { CapabilityLibrary } from './CapabilityLibrary';
import { ExecutionProfiles } from './ExecutionProfiles';
import { RoutePanel, type RouteStatus } from './RoutePanel';
import type { ExecutionChange } from './ExecutionChangeDetails';
import { CapabilityReferences, ExecutionDetails, type CapabilityAsset, type CapabilityEvidence, type SkillEvidence, type ExecutionSnapshot } from './ExecutionDetails';

type Route = { id: string; instance_id: string; model: string; endpoint: string; config_revision: string };
type Message = { id: string; turn_id: string; role: string; text: string; status: string };
type Session = {
  id: string; title: string; project_id: string; status: string; connection: string; origin: string;
  paused: boolean; archived: boolean; control_mode: string; permission: string; updated_at: string;
  generation: number; access?: SessionAccess; native_binding?: { bound_at: string; metadata: { source: string; native_status: string; title: string } };
  execution_snapshot?: ExecutionSnapshot; pending_execution_change?: ExecutionChange; execution_hold?: boolean; execution_history?: ExecutionSnapshot[]; execution_change_attempts?:{change:ExecutionChange;recovered_at:string}[];
  scope: { native_id: string; state_namespace?: string; cwd: string; route: Route; instance: { id: string; kind: string } };
  view: { configuration_failure?: ConfigurationFailure; settings_guard?: SettingsGuard; native_status?: string; capability_evidence?: CapabilityEvidence[]; skill_evidence?: SkillEvidence[]; plugin_evidence?: PluginEvidence[]; hook_executions?: HookExecution[]; terminal_command?: string; messages: Message[]; pending: PendingAction[]; executions?: { id: string; kind: string; status: string }[]; evidence?: { detail: string; source: string; at: string; complete: boolean } };
};
type Operation = { id: string; session_id: string; status: string; error?: string; intent: { kind: string }; evidence: { source: string; detail: string; at: string }[] | null };
type Snapshot = { capability_assets?: CapabilityAsset[]; route_statuses: RouteStatus[]; instances: InstanceStatus[]; sequence: number; sessions: Session[]; operations: Operation[]; routes: Route[]; projects: { id: string; name: string; cwd: string }[]; error?: string };
type SessionRead = { sequence: number; session: Session; operations: Operation[]; events: DomainEvent[]; total_messages: number; previous_cursor?: string };

function mergeRead(previous: SessionRead | undefined, next: SessionRead): SessionRead {
  if (!previous || previous.session.id !== next.session.id || previous.total_messages > next.total_messages) return next;
  if (previous.sequence > next.sequence) return previous;
  const before = previous.session.view.messages;
  const after = next.session.view.messages;
  const previousKeys = new Set(before.map((m) => `${m.turn_id}/${m.id}`));
  if (before.length && next.previous_cursor && !after.some((m) => previousKeys.has(`${m.turn_id}/${m.id}`))) return next;
  const messages = [...new Map([...before, ...after].map((m) => [`${m.turn_id}/${m.id}`, m])).values()];
  return { ...next, previous_cursor: before.length ? previous.previous_cursor : next.previous_cursor,
    session: { ...next.session, view: { ...next.session.view, messages } } };
}
const labels: Record<string, string> = { starting: '正在创建', idle: '空闲', running: '执行中', unknown: '待核对', waiting_approval: '等待审批', waiting_input: '等待回答', closed: '已关闭', prepared: '已登记', dispatching: '正在发送', acknowledged: '已接受', uncertain: '待核对', succeeded: '已完成', failed: '失败', interrupted: '已中止', not_sent: '确认未发送' };
class RejectedRequest extends Error {}
async function request<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(path, { method: body ? 'POST' : 'GET', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined });
  const value = await response.json();
  if (!response.ok) throw new RejectedRequest(value.error ?? `请求失败 (${response.status})`);
  return value;
}

function Console() {
  const [snapshot, setSnapshot] = useState<Snapshot>();
  const [detail, setDetail] = useState<SessionRead>();
  const [loadingHistory, setLoadingHistory] = useState(false);
  const [selected, setSelected] = useState(() => sessionStorage.getItem('netcap.selected.v1') ?? '');
  const [connected, setConnected] = useState(false);
  const [authorized, setAuthorized] = useState(false);
  const [token, setToken] = useState(() => new URLSearchParams(location.hash.slice(1)).get('token') ?? '');
  const [error, setError] = useState('');
  const [receipt, setReceipt] = useState('');
  const [localReceipt, setLocalReceipt] = useState<{ sessionId: string; text: string }>();
  const [creating, setCreating] = useState(false);
  const [createRoute, setCreateRoute] = useState('');
  const [routesView, setRoutesView] = useState(false);
  const [profilesView, setProfilesView] = useState(false);
  const [libraryView, setLibraryView] = useState(false);
  const [nativeView, setNativeView] = useState(false);
  const [busy, setBusy] = useState(false);
  const [draft, setDraft] = useState('');
  const [skillChoice, setSkillChoice] = useState<SkillInputChoice>();
  const [details, setDetails] = useState(false);
  const sending = useRef(false);
  const stream = useRef<HTMLDivElement>(null);
  const followLatest = useRef(true);
  const previousHeight = useRef<number | undefined>(undefined);
  const session = detail?.session.id === selected ? detail.session : undefined;
  const chosenSkills = skillChoice?.snapshotId === session?.execution_snapshot?.id ? skillChoice?.items ?? [] : [];
  const readOnlySource = session?.control_mode === 'observed' || session?.origin === 'native';
  const operations = session ? detail?.operations ?? [] : [];
  const lastOperation = operations.at(-1);
  const instance = snapshot?.instances.find((item) => item.id === session?.scope.instance.id);
  const selectedRoute = createRoute || snapshot?.routes.find((route) => supports(snapshot.instances.find((item) => item.id === route.instance_id), 'create'))?.id || '';
  const createInstance = snapshot?.instances.find((item) => item.id === snapshot.routes.find((route) => route.id === selectedRoute)?.instance_id);

  useEffect(() => { sessionStorage.setItem('netcap.selected.v1', selected); followLatest.current = true; }, [selected]);
  useEffect(() => { setSkillChoice(undefined); }, [selected, session?.execution_snapshot?.id]);

  useEffect(() => {
    history.replaceState(null, '', location.pathname);
    let ignore = false;
    request<Snapshot>('/api/workspace').then((value) => { if (!ignore) { setSnapshot(value); setAuthorized(true); } }).catch(() => {});
    return () => { ignore = true; };
  }, []);

  async function login(event?: FormEvent) {
    event?.preventDefault(); setError('');
    try { await request('/api/login', { token }); setToken(''); setAuthorized(true); }
    catch (e) { setError((e as Error).message); }
  }

  useEffect(() => {
    if (!authorized) return;
    let ignore = false;
    let inFlight = false;
    let pending = false;
    async function refresh() {
      if (inFlight) { pending = true; return; }
      inFlight = true;
      do {
        pending = false;
        try {
          const data = await request<Snapshot>('/api/workspace');
          if (!ignore) { setSnapshot((old) => !old || data.sequence >= old.sequence ? data : old); setConnected(navigator.onLine); }
          if (selected && data.sessions.some((s) => s.id === selected)) {
            const next = await request<SessionRead>(`/api/sessions/${encodeURIComponent(selected)}`);
            if (!ignore) setDetail((old) => mergeRead(old, next));
          }
        } catch (e) { if (!ignore) { setConnected(false); setError((e as Error).message); } }
      } while (pending && !ignore);
      inFlight = false;
    }
    void refresh();
    const source = new EventSource('/api/events');
    source.addEventListener('change', () => void refresh());
    source.addEventListener('snapshot', () => void refresh());
    source.onopen = () => { setConnected(navigator.onLine); void refresh(); };
    source.onerror = () => { if (!ignore) setConnected(false); };
    const offline = () => setConnected(false);
    const online = () => void refresh();
    window.addEventListener('offline', offline);
    window.addEventListener('online', online);
    return () => { ignore = true; source.close(); window.removeEventListener('offline', offline); window.removeEventListener('online', online); };
  }, [authorized, selected]);

  useLayoutEffect(() => {
    const element = stream.current;
    if (!element) return;
    if (previousHeight.current !== undefined) { element.scrollTop += element.scrollHeight - previousHeight.current; previousHeight.current = undefined; }
    else if (followLatest.current) element.scrollTop = element.scrollHeight;
  }, [session?.view.messages]);

  async function loadOlder() {
    if (!detail?.previous_cursor || loadingHistory) return;
    const id = selected;
    setLoadingHistory(true); setError('');
    try {
      const older = await request<SessionRead>(`/api/sessions/${encodeURIComponent(id)}?before=${encodeURIComponent(detail.previous_cursor)}`);
      followLatest.current = false;
      previousHeight.current = stream.current?.scrollHeight;
      setDetail((current) => {
        if (!current || current.session.id !== id) return current;
        const messages = [...new Map([...older.session.view.messages, ...current.session.view.messages].map((m) => [`${m.turn_id}/${m.id}`, m])).values()];
        return { ...current, previous_cursor: older.previous_cursor, session: { ...current.session, view: { ...current.session.view, messages } } };
      });
    } catch (e) { setError((e as Error).message); }
    finally { setLoadingHistory(false); }
  }

  async function command(fields: CommandFields) {
    if (sending.current) return;
    sending.current = true; setBusy(true); setError(''); setLocalReceipt(undefined);
    try {
      const bytes = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(JSON.stringify(fields)));
      const hash = Array.from(new Uint8Array(bytes), (n) => n.toString(16).padStart(2, '0')).join('');
      const saved = JSON.parse(sessionStorage.getItem('netcap.command.v1') ?? 'null') as { id: string; hash: string } | null;
      const id = saved?.hash === hash ? saved.id : crypto.randomUUID();
      // Store only the id/hash. Retrying a lost HTTP receipt uses the same id;
      // message bodies and secret answers are not written to browser storage.
      sessionStorage.setItem('netcap.command.v1', JSON.stringify({ id, hash }));
      const result = await request<{ session_id: string; status?: string }>('/api/commands', { command_id: id, ...fields });
      sessionStorage.removeItem('netcap.command.v1');
      if (result.status === 'pending' || result.status === 'discarded') setLocalReceipt({ sessionId: result.session_id, text: result.status === 'pending' ? '待应用选择已保存，当前生效配置不变' : '待应用选择已撤销' });
      setSelected(result.session_id); setProfilesView(false); setLibraryView(false); setNativeView(false); setRoutesView(fields.kind === 'probe'); setReceipt(result.status === 'cached' ? '复用 TTL 内的成功证据' : result.status === 'coalesced' ? '已合并到在途探测' : '已登记，等待原生执行结果');
      if (fields.kind === 'send') { setDraft(''); setSkillChoice(undefined); }
      setCreating(false);
    } catch (e) { if (e instanceof RejectedRequest) sessionStorage.removeItem('netcap.command.v1'); setError((e as Error).message); }
    finally { sending.current = false; setBusy(false); }
  }

  function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const form = new FormData(event.currentTarget);
    const selections = [...form.entries()].filter(([key, value]) => key.startsWith('capability_selection:') && value !== 'inherit')
      .map(([key, value]) => { const assetId = key.slice('capability_selection:'.length); const review = form.get(`capability_hook_review:${assetId}`); return { asset_id: assetId, mode: String(value), ...(review ? { hook_review: JSON.parse(String(review)) } : {}) }; });
    void command({ kind: 'create', title: String(form.get('title')), project_id: String(form.get('project')), route_id: String(form.get('route')), permission: String(form.get('permission')), capability_refs: form.getAll('capability_ref').map(String), capability_selections: selections });
  }

  if (!authorized) return <main className="login"><span className="wordmark">netcap /</span><h1>连接本地工作台</h1><p>使用此控制台状态目录内的本地访问令牌。</p><form onSubmit={(e) => void login(e)}><label>访问令牌<input type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} required /></label><button className="primary">连接工作台</button></form>{error ? <p role="alert" className="error">{error}</p> : null}</main>;

  return <div className="shell">
    <header className="topbar"><span className="wordmark">netcap <span>/ 工作台</span></span><div><span className={`connection ${connected ? '' : 'warning'}`}>{connected ? '控制台已连接' : '连接断开 · 数据已过期'}</span><span className="stage">M2 · Alpha</span></div></header>
    <aside className="sidebar"><div className="section-heading"><div><span className="eyebrow">WORKSPACE</span><h2>会话</h2></div><button title="新建会话" onClick={() => setCreating(true)} disabled={!connected}>＋ 新建</button></div>
      <button className="routes-toggle" aria-pressed={routesView} onClick={() => { setRoutesView(!routesView); setProfilesView(false); setLibraryView(false); setNativeView(false); }}>供应商路由</button>
      <button className="routes-toggle" aria-pressed={profilesView} onClick={() => { setProfilesView(!profilesView); setRoutesView(false); setLibraryView(false); setNativeView(false); }}>执行配置</button>
      <button className="routes-toggle" aria-pressed={libraryView} onClick={() => { setLibraryView(!libraryView); setProfilesView(false); setRoutesView(false); setNativeView(false); }}>能力库</button>
      <button className="routes-toggle" aria-pressed={nativeView} onClick={() => { setNativeView(!nativeView); setProfilesView(false); setLibraryView(false); setRoutesView(false); }}>发现原生会话</button>
      <SessionList sessions={snapshot?.sessions ?? []} projects={snapshot?.projects ?? []} selected={selected} labels={labels} select={(id) => { setSelected(id); setRoutesView(false); setProfilesView(false); setLibraryView(false); setNativeView(false); setReceipt(''); }} />
      <footer className="sidebar-note">会话与执行状态独立记录。<br />关闭页面后，后台继续工作。</footer>
    </aside>
    <main className="conversation">{nativeView ? <NativeSessionBrowser key={JSON.stringify([snapshot?.instances, snapshot?.projects])} instances={snapshot?.instances ?? []} projects={snapshot?.projects ?? []} connected={connected} read={request} openSession={id => { setSelected(id); setNativeView(false); }} /> : libraryView ? <CapabilityLibrary catalogKey={JSON.stringify(snapshot?.capability_assets ?? [])} instances={snapshot?.instances ?? []} connected={connected} read={request} selectSession={(id) => { setSelected(id); setLibraryView(false); setRoutesView(false); setProfilesView(false); }} /> : profilesView ? <ExecutionProfiles instances={snapshot?.instances ?? []} routes={snapshot?.route_statuses ?? []} connected={connected} read={request} useRoute={(id) => { setCreateRoute(id); setCreating(true); }} /> : routesView ? <RoutePanel routes={snapshot?.route_statuses ?? []} disabled={busy || !connected} send={command} select={(id) => { setSelected(id); setRoutesView(false); }} /> : session ? <>
      <div className="conversation-header"><div><span className="eyebrow">{session.scope.instance.kind} · {readOnlySource ? session.access?.source_label : session.scope.route.id}</span><h1>{session.title}</h1><p className="muted">{readOnlySource ? '原生执行与输入权未验证' : session.scope.route.model} <span className="divider">/</span> <span className={`badge ${session.status === 'unknown' ? 'warning' : ''}`}>{labels[session.status] ?? session.status}</span>{session.paused && !readOnlySource ? ' · 自动化已暂停' : ''}{session.archived ? ' · 已归档' : ''}</p></div><button className="details-toggle" onClick={() => setDetails(!details)}>详情</button></div>
      {session.origin === 'native' ? <p className="banner">已登记原生只读来源；按原 ID 查看历史，原执行控制仍未验证。</p> : readOnlySource ? <p className="banner">仅接收活动事件，不提供控制权、完整原生历史或可信路由信息。请在原终端继续操作。</p> : session.connection !== 'ready' ? <p className="banner warning">{session.connection === 'incompatible' ? '实例版本未验证或配置不可用，请检查执行详情并返回原终端处理。' : `原生连接${session.connection === 'disconnected' ? '已断开' : '正在核对'}，发送暂不可用。`}</p> : null}
      {session.view.pending?.filter((action) => ['pending', 'submitted', 'unverified'].includes(action.status)).map((action) => <PendingPanel key={action.id} action={action} sessionId={session.id} disabled={busy || !connected || session.execution_hold || session.connection !== 'ready' || readOnlySource || session.origin === 'probe' || !supports(instance, action.response_kind === 'approval' ? 'approval' : 'question')} send={command} />)}
      {session.origin === 'native' ? <div className="messages native-browser">{['codex', 'claude'].includes(session.scope.instance.kind) ? <NativeObservation key={session.id} sessionId={session.id} kind={session.scope.instance.kind} connected={connected} read={request} /> : null}<NativeHistory key={session.id} boundSessionId={session.id} instanceId={session.scope.instance.id} projectId={session.project_id} nativeId={session.scope.native_id} connected={connected} read={request} /></div> : <div className="messages" ref={stream} aria-live="polite" onScroll={(event) => { const element = event.currentTarget; followLatest.current = element.scrollHeight - element.clientHeight - element.scrollTop < 80; }}>{detail?.previous_cursor ? <button className="older-history" onClick={() => void loadOlder()} disabled={loadingHistory || !connected}>{loadingHistory ? '正在加载…' : '加载更早的消息'}</button> : null}{session.view.messages?.length ? session.view.messages.map((m) => <article className={`message ${m.role}`} key={`${m.turn_id}/${m.id}`}><span className="message-author">{m.role === 'user' ? '你' : m.role === 'assistant' ? session.scope.instance.kind : m.role === 'event' ? '观察事件' : '工具'}</span>{m.role === 'tool' ? <details><summary>工具输出</summary><pre>{m.text}</pre></details> : <div className="message-text">{m.text}</div>}</article>) : <div className="conversation-empty"><span className="eyebrow">{session.status === 'unknown' ? '等待原生证据' : 'READY WHEN YOU ARE'}</span><h2>{session.status === 'unknown' ? '尚无已核对的历史。' : '从一个明确的问题开始。'}</h2><p>{session.status === 'unknown' ? '请查看操作记录与原生身份，重新核对或返回原终端处理。' : '消息会发送到所选路由，执行结果与原生历史保持关联。'}</p></div>}</div>}
      {!readOnlySource && session.origin !== 'probe' ? <form className="composer" onSubmit={(e) => { e.preventDefault(); void command({ kind: 'send', session_id: session.id, text: draft, ...(chosenSkills.length ? { skill_input: { snapshot_id: session.execution_snapshot!.id, items: chosenSkills } } : {}) }); }}><SkillInputPicker snapshot={session.execution_snapshot} selected={chosenSkills} disabled={busy || !connected || session.connection !== 'ready' || session.status !== 'idle' || !!session.execution_hold} onChange={setSkillChoice} /><label className="sr" htmlFor="message">消息</label><textarea id="message" placeholder="发送一条消息…" value={draft} onChange={(e) => setDraft(e.target.value)} rows={3} disabled={readOnlySource} /><div className="composer-footer"><span role="status">{localReceipt?.sessionId === session.id ? localReceipt.text : lastOperation ? labels[lastOperation.status] ?? lastOperation.status : receipt || '发送后以原生终态确认结果'}</span><button className="primary" disabled={busy || !connected || session.connection !== 'ready' || session.status !== 'idle' || readOnlySource || !supports(instance, 'send') || !draft.trim()}>发送消息 <span aria-hidden="true">↗</span></button></div></form> : <p className="observed-footer">{session.origin === 'probe' ? '此会话执行已登记的路由探测，不接受额外交互输入。' : session.origin === 'native' ? '此会话只读绑定原生历史；执行控制、权限和供应商绑定仍待核对。' : '此会话仅观察。原生会话 ID 与事件来源可在执行详情中查看。'}</p>}
    </> : selected && snapshot?.sessions.some((s) => s.id === selected) ? <div className="welcome" role="status">正在加载会话历史…</div> : <div className="welcome"><span className="eyebrow">LOCAL AGENT CONSOLE</span><h1>你的工作，<br />在这里接续。</h1><p>集中查看会话、消息和每一次执行的真实状态。</p><button className="primary" onClick={() => setCreating(true)} disabled={!connected}>创建第一个会话</button></div>}</main>
    <aside className={`inspector ${details ? 'visible' : ''}`}><div className="section-heading"><h2>执行详情</h2><button className="details-toggle" onClick={() => setDetails(false)}>关闭</button></div>{session && !routesView && !profilesView && !libraryView && !nativeView ? <><dl><dt>控制方式</dt><dd>{session.control_mode === 'managed' ? '托管会话' : session.origin === 'native' ? '只读原生来源' : readOnlySource ? '仅观察' : '人工接管'}</dd><dt>原生会话 ID</dt><dd className="mono">{session.scope.native_id || '尚未取得'}</dd><dt>原生状态归属</dt><dd className="mono">{session.scope.state_namespace || '旧记录尚无状态目录绑定证据'}</dd><dt>工作目录</dt><dd className="mono">{session.scope.cwd}</dd><dt>绑定路由</dt><dd>{session.scope.route.id || '未验证'}<small className="mono">{session.scope.route.endpoint}</small></dd><dt>权限</dt><dd>{readOnlySource ? '原生权限未验证' : session.permission === 'read_only' ? '只读 · 按需审批' : '工作区写入 · 按需审批'}</dd></dl>{!readOnlySource ? <div className="inspector-actions"><button disabled={busy || !connected || session.execution_hold || (session.paused && (!supports(instance, 'resume') || session.origin === 'probe'))} onClick={() => void command({ kind: session.paused ? 'resume' : 'pause', session_id: session.id })}>{session.paused ? '恢复自动化' : '暂停自动化'}</button><button disabled={busy || !connected} onClick={() => void command({ kind: 'takeover', session_id: session.id })}>转人工</button>{['running', 'waiting_approval', 'waiting_input'].includes(session.status) ? <button disabled={busy || !connected || !supports(instance, 'interrupt')} onClick={() => void command({ kind: 'interrupt', session_id: session.id })}>停止当前轮次</button> : null}{session.status === 'unknown' ? <button disabled={busy || !connected} onClick={() => void command({ kind: 'reconcile', session_id: session.id })}>重新核对</button> : null}</div> : <p className="hint">此只读来源不能授予发送、停止、回复或接管权限。</p>}{!readOnlySource ? <p className="hint">恢复自动化会先核对原会话、在途工具和未决请求。请先处理原终端草稿并停止输入；暂停或转人工不会清除未知占用。</p> : null}<SessionAccessDetails key={JSON.stringify([session.id, session.generation, session.connection, session.status, session.access?.context?.binding, session.access?.context?.registry_status])} sessionId={session.id} access={session.access} connected={connected} read={request} />{instance ? <InstanceDetails instance={instance} /> : null}{session.origin !== 'native' ? <ExecutionDetails assets={snapshot?.capability_assets ?? []} key={session.id} sessionId={session.id} generation={session.generation} snapshot={session.execution_snapshot} evidence={session.view.capability_evidence} skillEvidence={session.view.skill_evidence} pluginEvidence={session.view.plugin_evidence} hookExecutions={session.view.hook_executions} settingsGuard={session.view.settings_guard} configurationFailure={session.view.configuration_failure} connected={connected} read={request} pending={session.pending_execution_change} busy={busy} command={command} hold={session.execution_hold ?? false} history={session.execution_history} attempts={session.execution_change_attempts} nativeStatus={session.view.native_status} sessionStatus={session.status} /> : null}{session.view.executions?.length ? <div className="execution-list"><h3>仍在占用的执行</h3>{session.view.executions.map((execution) => <p key={`${execution.kind}/${execution.id}`}>{execution.kind} · {execution.status}<small className="mono">{execution.id}</small></p>)}</div> : null}<details className="native-location"><summary>返回原终端</summary><p>{session.origin === 'native' ? '请回到原执行所在的终端，按上方状态目录和原生 ID 核对。此登记没有改变原终端或生成控制命令。' : '此命令连接当前实例和原生会话。请在已配置该路由凭据的终端中执行。'}</p>{session.view.terminal_command ? <><pre>{session.view.terminal_command}</pre><button onClick={() => { void navigator.clipboard.writeText(session.view.terminal_command!).then(() => setReceipt('终端命令已复制')).catch(() => setError('复制失败，请手动选择命令复制')); }}>复制终端命令</button></> : <p>{session.origin === 'native' ? '原执行连接方式仍需单独验证。' : readOnlySource ? '请返回提供该事件的原终端，使用上方原生会话 ID 定位。' : '取得原生会话 ID 后可复制继续命令。'}</p>}</details><button className="archive-session" disabled={!connected || busy} onClick={() => void command({ kind: session.archived ? 'unarchive' : 'archive', session_id: session.id })}>{session.archived ? '重新显示会话' : '归档会话'}</button><p className="hint">归档只隐藏本地列表中的会话，原生执行和历史仍保留。</p><EventTimeline events={detail?.events ?? []} /><h3>操作记录</h3>{operations.map((op) => <details className="operation" key={op.id}><summary>{op.intent.kind} · {labels[op.status] ?? op.status}</summary>{op.error ? <p className="error">{op.error}</p> : null}<code>{op.id}</code>{(op.evidence ?? []).slice(-3).map((e, n) => <p key={n}>{e.detail}<small>{e.source}</small></p>)}{op.status === 'uncertain' && ['create', 'send'].includes(op.intent.kind) ? <form className="associate-form" onSubmit={(event) => { event.preventDefault(); const fields = new FormData(event.currentTarget); void command({ kind: 'associate', session_id: session.id, operation_id: op.id, native_id: String(fields.get('native')), native_turn: String(fields.get('turn') ?? '') }); }}><label>候选原生会话 ID<input name="native" defaultValue={session.scope.native_id} required /></label><label>候选轮次 ID（可选）<input name="turn" /></label><button disabled={busy || !connected || !supports(instance, 'recovery')}>验证并关联原执行</button><p>只核对原生身份和输入；不会发送新任务或直接清除未知占用。</p></form> : null}</details>)}</> : <p className="muted">选择会话后查看路由、原生身份和执行证据。</p>}</aside>
    {error || snapshot?.error ? <div className="toast error" role="alert">{error || snapshot?.error}<button onClick={() => setError('')} aria-label="关闭提示">×</button></div> : null}
    {creating ? <div className="overlay" onClick={(e) => { if (e.target === e.currentTarget) setCreating(false); }}><section className="dialog" role="dialog" aria-modal="true" aria-labelledby="create-title"><div className="section-heading"><h2 id="create-title">新建会话</h2><button onClick={() => setCreating(false)} aria-label="关闭新建会话">×</button></div><p className="muted">明确选择工作目录、供应商路由和权限。</p>{snapshot?.instances.map((item) => <InstanceDetails key={item.id} instance={item} />)}<form onSubmit={create}><label>会话名称<input name="title" autoFocus placeholder="例如：整理项目接口" maxLength={80} /></label><label>项目<select name="project" required>{snapshot?.projects.map((p) => <option key={p.id} value={p.id}>{p.name || p.id} · {p.cwd}</option>)}</select></label><label>路由<select name="route" required value={selectedRoute} onChange={(event) => setCreateRoute(event.target.value)}><option value="" disabled>选择可用路由</option>{snapshot?.routes.map((r) => <option key={r.id} value={r.id} disabled={!supports(snapshot.instances.find((item) => item.id === r.instance_id), 'create')}>{snapshot.instances.find((item) => item.id === r.instance_id)?.name || r.instance_id} · {r.id} · {r.model}{supports(snapshot.instances.find((item) => item.id === r.instance_id), 'create') ? '' : ' · 实例未验证或不可用'}</option>)}</select></label><label>权限策略<select name="permission" defaultValue="read_only"><option value="read_only">只读 · 按需审批</option><option value="workspace">工作区写入 · 按需审批</option></select></label><CapabilityReferences key={selectedRoute} assets={snapshot?.capability_assets ?? []} backend={createInstance?.kind ?? ""} instanceId={createInstance?.id ?? ""} connected={connected} read={request} /><button className="primary" disabled={busy || !snapshot?.projects.length || !snapshot.routes.length || !supports(createInstance, 'create')}>创建会话</button></form></section></div> : null}
  </div>;
}

createRoot(document.getElementById('root')!).render(<StrictMode><Console /></StrictMode>);
