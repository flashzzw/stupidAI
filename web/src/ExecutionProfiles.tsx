import { useEffect, useRef, useState, type FormEvent } from 'react';
import { InstanceDetails, supports, type InstanceStatus } from './InstanceDetails';
import type { RouteStatus } from './RoutePanel';

type Profile = {
  instance: { id: string; name: string; kind: string; executable: string; profile: string; settings_file?: string;
    socket?: string; bridge?: string; node_executable?: string; policy_revision: string; env_allow: string[] };
  route: { id: string; instance_id: string; provider: string; endpoint: string; model: string;
    credential_ref: string; credential_revision: string; config_revision: string };
};
type Preview = { profile: Profile; instance_status: InstanceStatus; route_status: RouteStatus; control_detail: string };
type Read = <T>(path: string, body?: unknown) => Promise<T>;

export function ExecutionProfiles({ instances, routes, connected, read, useRoute }: {
  instances: InstanceStatus[]; routes: RouteStatus[]; connected: boolean; read: Read; useRoute: (routeId: string) => void;
}) {
  const [adding, setAdding] = useState(false);
  const [diagnosing, setDiagnosing] = useState(false);
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');
  async function diagnose() {
    setDiagnosing(true); setError('');
    try { await read('/api/execution-profiles/diagnose', {}); setNotice('诊断已刷新，未发送模型请求。'); }
    catch (e) { setError((e as Error).message); }
    finally { setDiagnosing(false); }
  }
  return <section className="execution-profiles" aria-label="执行配置管理">
    <div className="section-heading"><div><span className="eyebrow">EXECUTION PROFILES</span><h1>执行配置</h1></div>
      <button className="primary" disabled={!connected} onClick={() => setAdding(true)}>登记执行配置</button></div>
    <p className="hint">分别绑定 CLI、原生状态目录与供应商路由。名称可以相同；实际目录相同的配置共享原生状态。</p>
    <button disabled={!connected || diagnosing} onClick={() => void diagnose()}>{diagnosing ? '正在诊断…' : '重新诊断配置'}</button>
    {notice ? <p role="status" className="hint">{notice}</p> : null}
    {error ? <p role="alert" className="error">{error}</p> : null}
    {instances.map((instance) => <article className="execution-profile" key={instance.id}>
      <h2>{instance.name || instance.id}</h2><p className="muted">{instance.kind} · {instance.id}</p>
      <p className="mono">{instance.state_namespace || instance.profile}</p>
      {instance.shared_state_with?.length ? <p className="warning">与 {instance.shared_state_with.join('、')} 共享原生状态目录</p> : null}
      {instance.config_error ? <p className="error">{instance.config_error}</p> : null}
      <InstanceDetails instance={instance} />
      {routes.filter(({ route }) => route.instance_id === instance.id).map(({ route, config_error }) => <div className="execution-profile-route" key={route.id}>
        <p>{route.id} · {route.model}</p><p className="mono">{route.endpoint}</p>
        <p className="hint">凭据引用：{route.credential_ref} · 版本 {route.credential_revision}</p>
        {config_error ? <p className="error">{config_error}</p> : null}
        <button disabled={!connected || !!config_error || !supports(instance, 'create')} onClick={() => useRoute(route.id)}>使用此配置</button>
      </div>)}
    </article>)}
    {!instances.length ? <p className="hint">尚未登记执行配置。添加已有 CLI 和原生配置目录后，可创建会话。</p> : null}
    {adding ? <Registration instances={instances} connected={connected} read={read} close={() => setAdding(false)} saved={(id) => {
      setAdding(false); setNotice(`已登记 ${id}；可在下方或新建会话中选用。`);
    }} /> : null}
  </section>;
}

function Registration({ instances, connected, read, close, saved }: {
  instances: InstanceStatus[]; connected: boolean; read: Read; close: () => void; saved: (id: string) => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [backend, setBackend] = useState('codex');
  const [preview, setPreview] = useState<Preview>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const template = instances.find((i) => i.kind === backend);
  useEffect(() => { dialog.current?.showModal(); }, []);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true); setError('');
    const data = new FormData(event.currentTarget);
    const value = (name: string) => String(data.get(name) ?? '').trim();
    const id = value('instance_id');
    const profile: Profile = {
      instance: { id, name: value('name'), kind: backend, executable: value('executable'), profile: value('profile'),
        policy_revision: value('policy_revision'), env_allow: value('env_allow').split(/[\s,]+/).filter(Boolean),
        ...(backend === 'codex' ? { socket: value('socket') } : { bridge: value('bridge'), node_executable: value('node_executable'),
          ...(value('settings_file') ? { settings_file: value('settings_file') } : {}) }) },
      route: { id: value('route_id'), instance_id: id, provider: value('provider'), endpoint: value('endpoint'), model: value('model'),
        credential_ref: value('credential_ref'), credential_revision: value('credential_revision'), config_revision: value('config_revision') },
    };
    try { setPreview(await read<Preview>('/api/execution-profiles/preview', profile)); }
    catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  }
  async function register() {
    if (!preview) return;
    setBusy(true); setError('');
    const payload = JSON.stringify(preview.profile);
    const key = 'netcap.execution-registration.v1';
    try {
      const pending = JSON.parse(sessionStorage.getItem(key) ?? 'null') as { payload: string; id: string } | null;
      const id = pending?.payload === payload ? pending.id : crypto.randomUUID();
      sessionStorage.setItem(key, JSON.stringify({ payload, id }));
      const result = await read<{ execution_profile_id: string }>('/api/commands', { command_id: id, kind: 'register_execution', execution_profile: preview.profile });
      sessionStorage.removeItem(key); saved(result.execution_profile_id);
    } catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  }
  return <dialog ref={dialog} className="dialog registration-dialog" onClose={close} aria-labelledby="registration-title">
    <div className="section-heading"><h2 id="registration-title">登记执行配置</h2><button onClick={close} aria-label="关闭执行配置登记">×</button></div>
    <p className="muted">使用已有目录与凭据环境变量。登记保存在控制台中；更换来源时使用新的 ID 与版本。</p>
    <form onSubmit={(event) => void submit(event)} onChange={() => { setPreview(undefined); setError(''); }}>
      <fieldset disabled={busy}><legend>原生来源</legend>
        <label>执行后端<select value={backend} onChange={(event) => setBackend(event.target.value)}><option value="codex">Codex</option><option value="claude">Claude Code</option></select></label>
        <label>配置名称<input name="name" maxLength={200} placeholder="例如：日常开发" /></label>
        <label>实例 ID<input name="instance_id" required pattern="(?:[A-Za-z0-9_]|-)+" maxLength={100} /></label>
        <label key={`executable-${backend}`}>CLI 可执行文件<input name="executable" required defaultValue={template?.executable} placeholder="绝对路径" /></label>
        <label>原生状态目录<input name="profile" required placeholder="绝对路径" /></label>
        {backend === 'codex' ? <label>控制 socket<input name="socket" required placeholder="已有 Codex 后台的 Unix socket 绝对路径" /></label> : <>
          <label>自定义 settings 文件（可选）<input name="settings_file" placeholder="绝对路径；为空时使用原生默认来源" /></label>
          <p className="hint">自定义 settings 与状态目录分开绑定；用户、项目和本地配置继续按原生规则继承。</p>
          <label>SDK bridge 文件<input name="bridge" required defaultValue={template?.bridge} placeholder="已构建 bridge 的绝对路径" /></label>
          <label>Node 可执行文件（可选）<input name="node_executable" defaultValue={template?.node_executable} placeholder="绝对路径；为空使用控制台 PATH" /></label>
        </>}
        <label>策略版本<input name="policy_revision" required defaultValue="1" /></label>
        <label>额外传入的环境变量名（可选）<input name="env_allow" placeholder="逗号或空格分隔，只填写名称" /></label>
      </fieldset>
      <fieldset disabled={busy}><legend>供应商路由</legend>
        <label>路由 ID<input name="route_id" required pattern="(?:[A-Za-z0-9_]|-)+" maxLength={100} /></label>
        <label>供应商<input name="provider" required /></label>
        <label>API 端点<input name="endpoint" type="url" required placeholder="https://…" /></label>
        <label>模型<input name="model" required /></label>
        <label>凭据引用<input name="credential_ref" required placeholder="env:VARIABLE_NAME" /></label>
        <label>凭据版本<input name="credential_revision" required defaultValue="1" /></label>
        <label>路由配置版本<input name="config_revision" required defaultValue="1" /></label>
      </fieldset>
      <button disabled={busy || !connected}>{busy ? '正在处理…' : '预览并诊断'}</button>
    </form>
    {preview ? <section className="registration-preview" aria-label="登记预览">
      <h3>登记预览</h3><InstanceDetails instance={preview.instance_status} />
      <p>{preview.profile.instance.id} → {preview.profile.route.id}</p><p className="mono">{preview.profile.route.endpoint}</p>
      <p>凭据引用：{preview.profile.route.credential_ref}</p><p className="hint">{preview.control_detail}</p>
      {preview.instance_status.config_error ? <p className="error">{preview.instance_status.config_error}</p> : null}
      {preview.route_status.config_error ? <p className="error">{preview.route_status.config_error}</p> : null}
      <p className="hint">不可用配置可以先登记，修复来源后重新诊断。此操作不创建会话、不发送模型请求。</p>
      <button className="primary" disabled={busy || !connected} onClick={() => void register()}>确认登记</button>
    </section> : null}
    {error ? <p role="alert" className="error">{error}</p> : null}
  </dialog>;
}
