export type ExecutorEvidence = {
  source: string; at: string; pid: number; birth: string; os: string; uid: string; user?: string;
  executable: string; wsl_distro?: string; detail: string;
};
export type SessionContext = {
  basis: string; registry_status: string; registry_detail: string; executor?: ExecutorEvidence;
  binding: { instance_id: string; name?: string; kind: string; executable?: string; profile?: string;
    settings_file?: string; endpoint?: string; bridge?: string; declared_version?: string;
    state_namespace?: string; cwd?: string; native_id?: string; permission: string };
};
const registryNames: Record<string, string> = { matching: '登记定义相同', changed: '登记定义已变化', missing: '当前登记缺失', isolated: '派生隔离配置' };
const permissionNames: Record<string, string> = { read_only: '只读模式', workspace: '工作区写入模式', unverified: '原权限未验证' };

export function ExecutorEvidenceDetails({ evidence }: { evidence?: ExecutorEvidence }) {
  if (!evidence) return <p className="hint">尚无对应后台的环境采样；原执行的 WSL 发行版和 Linux 用户仍未验证。</p>;
  return <section className="executor-evidence" aria-label="接入后台环境证据">
    <h3>接入后台采样</h3>
    <dl className="binding-facts"><div><dt>系统 / 本地用户</dt><dd>{evidence.os} · {evidence.user || '用户名未取得'}（UID {evidence.uid}）</dd></div>
      <div><dt>WSL 名称 · 进程环境声明</dt><dd>{evidence.wsl_distro || '未提供声明，不能据此判断是否为 WSL'}</dd></div>
      <div><dt>后台运行映像</dt><dd className="mono">{evidence.executable}</dd></div>
      <div><dt>进程身份</dt><dd className="mono">PID {evidence.pid} · 启动标识 {evidence.birth}</dd></div></dl>
    <p className="hint">{evidence.detail}</p><small>{evidence.source} · <time dateTime={evidence.at}>{new Date(evidence.at).toLocaleString()}</time> · 该时刻的采样</small>
  </section>;
}

export function SessionContextDetails({ context }: { context?: SessionContext }) {
  if (!context) return <p className="hint">此记录尚无结构化绑定说明；不能从目录名称推定执行环境。</p>;
  const binding = context.binding;
  const title = context.basis === 'selected_configuration' ? '本次查询使用的配置' : context.basis === 'saved_binding' ? '会话保存的绑定' : '记录中的上下文 · 原绑定未验证';
  const rows = [
    ['CLI 路径', binding.executable], ['状态 / 默认配置目录', binding.profile], ['自定义 settings', binding.settings_file],
    ['记录的状态命名空间', binding.state_namespace], ['接入端点', binding.endpoint], ['SDK bridge', binding.bridge], ['项目目录', binding.cwd],
  ];
  return <section className="session-context" aria-label={title}><h3>{title}</h3>
    <p>{binding.name || binding.instance_id || '未记录执行配置'} · {binding.kind || '后端未记录'}</p>
    <p className={context.registry_status === 'changed' || context.registry_status === 'missing' ? 'warning' : 'hint'}>{registryNames[context.registry_status] ?? '登记定义待核对'}</p>
    <p className="hint">{context.registry_detail}</p>
    <dl className="binding-facts"><div><dt>执行配置 ID</dt><dd className="mono">{binding.instance_id || '未记录'}</dd></div>
      {rows.filter(([, value]) => value).map(([label, value]) => <div key={label}><dt>{label}</dt><dd className="mono">{value}</dd></div>)}
      <div><dt>声明的 CLI 版本</dt><dd>{binding.declared_version || '未记录'}</dd></div>
      <div><dt>记录的权限选择</dt><dd>{permissionNames[binding.permission] ?? '未记录'}</dd></div></dl>
    <ExecutorEvidenceDetails evidence={context.executor} />
  </section>;
}
