import type { CommandFields } from './PendingAction';

export type RouteStatus = {
  route: { id: string; instance_id: string; provider: string; model: string; endpoint: string; credential_ref: string; credential_revision: string; config_revision: string };
  identity: string; config_fingerprint?: string; health: string; reason: string; config_error?: string;
  proof?: string; last_success_at?: string; expires_at?: string; probe_available: boolean; probe_reason?: string;
  next_allowed_at?: string; failure_kind?: string; admission_reason?: string;
  probe?: { id: string; session_id: string; mode: string; status: string; reason?: string; result?: string; started_at: string; finished_at?: string };
};

const healthNames: Record<string, string> = { unknown: '健康未验证', healthy: '最近调用成功', stale: '成功证据已过期', backoff: '等待退避', attention: '需要处理' };
const proofNames: Record<string, string> = { text: '仅验证生成能力', generation: '会话模型调用', 'utc-date': 'UTC 日期工具链' };
const probeNames: Record<string, string> = { starting: '正在创建探测会话', running: '探测执行中', stopping: '核对中止结果', waiting: '等待执行容量', unknown: '核对原执行中', succeeded: '探测通过', failed: '探测失败', cancelled: '探测已取消' };

export function RoutePanel({ routes, disabled, send, select }: { routes: RouteStatus[]; disabled: boolean; send: (fields: CommandFields) => Promise<void>; select: (id: string) => void }) {
  return <section className="route-panel" aria-label="供应商路由">
    <span className="eyebrow">供应商路由</span><h1>明确每次请求的去向。</h1>
    <p className="muted">实例、端点、模型和配置版本共同确定路由身份。控制连接可用不代表模型请求成功。</p>
    {routes.map(({ route, identity, config_fingerprint, health, reason, config_error, proof, last_success_at, expires_at, next_allowed_at, admission_reason, probe, probe_available, probe_reason }) => <article className="route-row" key={route.id}>
      <div className="section-heading"><h2>{route.id}</h2><span className={`badge ${health === 'healthy' ? '' : 'warning'}`}>{config_error ? '配置待处理' : healthNames[health] ?? health}</span></div>
      <dl><dt>实例 / 供应商</dt><dd>{route.instance_id} / {route.provider}</dd><dt>配置端点</dt><dd className="mono">{route.endpoint}</dd>
        <dt>指定模型</dt><dd>{route.model}</dd><dt>凭据来源</dt><dd className="mono">{route.credential_ref}</dd>
        <dt>配置 / 凭据 revision</dt><dd>{route.config_revision} / {route.credential_revision}</dd></dl>
      <p className={config_error ? 'error' : 'hint'}>{config_error || reason}</p>
      {last_success_at ? <p className="hint">最近成功：<time dateTime={last_success_at}>{new Date(last_success_at).toLocaleString()}</time> · {proofNames[proof ?? ''] ?? '历史成功记录'}</p> : null}
      {expires_at ? <p className="hint">健康有效期：<time dateTime={expires_at}>{new Date(expires_at).toLocaleString()}</time></p> : null}
      {next_allowed_at ? <p className="hint">下次允许探测：<time dateTime={next_allowed_at}>{new Date(next_allowed_at).toLocaleString()}</time></p> : null}
      {probe ? <div className="probe-result" role="status"><strong>{probeNames[probe.status] ?? probe.status}</strong><p>{probe.reason}</p>{probe.result ? <code>{probe.result}</code> : null}<button onClick={() => select(probe.session_id)}>查看探测会话</button></div> : null}
      <div className="route-actions"><button disabled={disabled || !!config_error || !probe_available || !!admission_reason} onClick={() => void send({ kind: 'probe', route_id: route.id, probe_mode: 'utc-date' })}>立即日期探测</button><button disabled={disabled || !!config_error || !probe_available || !!admission_reason} onClick={() => void send({ kind: 'probe', route_id: route.id, probe_mode: 'text' })}>仅验证生成能力</button></div>
      {admission_reason ? <p className="hint">{admission_reason}</p> : null}
      {probe_reason ? <p className="hint">{probe_reason}</p> : <p className="hint">使用独立会话，遵守人工占用与容量限制；有效 TTL 内复用已有成功证据。</p>}
      <details><summary>配置身份</summary><p className="mono">{identity}</p>{config_fingerprint ? <p className="mono">{config_fingerprint}</p> : null}</details>
    </article>)}
    {routes.length === 0 ? <p>尚未登记供应商路由，请检查控制台配置。</p> : null}
  </section>;
}
