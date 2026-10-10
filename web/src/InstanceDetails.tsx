export type InstanceStatus = {
  id: string; name?: string; settings_file?: string; bridge?: string; node_executable?: string; control_endpoint?: string; control_status?: string; control_detail?: string; kind: string; executable: string; profile: string; configured_version: string; config_error?: string;
  state_namespace?: string; shared_state_with?: string[];
  description: { version: string; compatibility: string; detail: string; capabilities: Record<string, string>;
    local_ipc?: { status: string; operation: string; error_code?: string; at: string; detail: string } };
};

const statusNames: Record<string, string> = { supported: '已验证', unsupported: '不支持', unverified: '未验证', unavailable: '不可用' };
const capabilityNames: Record<string, string> = {
  create: '创建', send: '发送', history: '历史', resume: '恢复', approval: '审批', question: '问题', interrupt: '中止',
  recovery: '故障核对', native_goal: '原生 Goal', dynamic_mcp: '动态 MCP', external_tui: '外部终端接续', probe: '隔离路由探测', route_evidence: '工作会话路由证据',
};

export function supports(instance: InstanceStatus | undefined, capability: string) {
  return !!instance && !instance.config_error && instance.description.compatibility === 'supported' && instance.description.capabilities[capability] === 'supported';
}

export function InstanceDetails({ instance }: { instance: InstanceStatus }) {
  return <details className="instance-details"><summary>{instance.name ? `${instance.name} · ` : ''}{instance.id} · {instance.config_error ? '配置不可用' : statusNames[instance.description.compatibility] ?? '未验证'}</summary>
    <dl><dt>实际 CLI 版本</dt><dd>{instance.description.version || '未取得'}</dd><dt>配置声明版本</dt><dd>{instance.configured_version || '未声明'}</dd>
      <dt>CLI 路径</dt><dd className="mono">{instance.executable}</dd><dt>原生状态 / 默认配置目录</dt><dd className="mono">{instance.profile}</dd>
      {instance.settings_file ? <><dt>自定义 settings 文件</dt><dd className="mono">{instance.settings_file}</dd></> : null}
      {instance.control_endpoint ? <><dt>控制端点</dt><dd className="mono">{instance.control_endpoint}</dd></> : null}
      {instance.state_namespace ? <><dt>实际状态目录</dt><dd className="mono">{instance.state_namespace}</dd></> : null}</dl>
    {instance.shared_state_with?.length ? <p className="hint">与 {instance.shared_state_with.join('、')} 共享原生状态目录；不同名称不代表会话隔离。</p> : null}
    {instance.config_error ? <p className="error">{instance.config_error}</p> : null}
    <p className="hint">{instance.description.detail}</p><p className="hint">{instance.control_detail}</p>
    {instance.description.local_ipc ? <section aria-label="本地 stdio 通道检查">
      <h3>本地 stdio 通道检查 · {({ passed: '环境检查通过', blocked: '环境限制', unverified: '未验证' } as Record<string, string>)[instance.description.local_ipc.status] ?? '未验证'}</h3>
      <p className={instance.description.local_ipc.status === 'blocked' ? 'error' : 'hint'}>{instance.description.local_ipc.detail}</p>
      <small>{instance.description.local_ipc.operation}{instance.description.local_ipc.error_code ? ` · ${instance.description.local_ipc.error_code}` : ''} · <time dateTime={instance.description.local_ipc.at}>{new Date(instance.description.local_ipc.at).toLocaleString()}</time></small>
      <p className="hint">这是工作台进程的环境检查，不代表 MCP 服务已连接或工具调用已获准。</p>
    </section> : null}
    <ul>{Object.entries(instance.description.capabilities ?? {}).map(([name, state]) => <li key={name}>{name === 'external_tui' && instance.kind === 'codex' ? '共享后台终端接续' : capabilityNames[name] ?? name}<span>{statusNames[state] ?? '未验证'}</span></li>)}</ul>
    <p className="hint">能力诊断核对 CLI 版本、来源文件及可用的本地环境检查；可在执行配置中重新诊断。连接和路由仍以会话证据为准。</p>
  </details>;
}
