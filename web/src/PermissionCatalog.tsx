import { useEffect, useRef, useState } from 'react';

type PermissionCatalogData = {
  native_id: string; cwd: string; query_id: string; configuration_id: string; at: string; sample_hash: string;
  managed_only: boolean; conflict_count: number;
  items: { behavior: string; source: string; rule: string; editability: string; not_in_effect: boolean; conflicting: boolean }[];
  directories: { path: string; source: string }[];
  errors: { file: string; field: string }[];
  limitations: string[];
};

const sources: Record<string, string> = { userSettings: '用户设置', projectSettings: '项目设置', localSettings: '项目本地设置',
  flagSettings: '当前查询设置', policySettings: '管理员受管', cliArg: 'CLI 参数', command: '命令', session: '会话临时规则',
  toolsNarrowing: '工具范围限制', mcpServerPolicy: 'MCP 服务策略', hostCredential: '宿主凭据策略' };
const behaviors: Record<string, string> = { allow: '允许规则', deny: '拒绝规则', ask: '询问规则' };
const locations: Record<string, string> = { persistent: '配置文件', session: '会话内存', readonly: '原生只读来源' };

// Rule spelling is meaningful. Show controls and bidi/zero-width characters
// explicitly instead of letting a stored rule disguise another rule's text.
const visible = (text: string) => text.replace(/\\/g, '\\\\').replace(/[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/gu, value =>
  `\\u{${value.codePointAt(0)!.toString(16).toUpperCase()}}`);
const sourceName = (source: string) => sources[source] ?? `未知来源（${visible(source)}）`;

export function PermissionCatalog({ sessionId, connected, read }: {
  sessionId: string; connected: boolean; read: <T>(path: string, body?: unknown) => Promise<T>;
}) {
  const [catalog, setCatalog] = useState<PermissionCatalogData>();
  const [comparison, setComparison] = useState('');
  const [source, setSource] = useState('');
  const [limit, setLimit] = useState(40);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const previous = useRef<PermissionCatalogData | undefined>(undefined);
  const request = useRef(0);
  useEffect(() => () => { request.current++; }, []);

  async function sample() {
    const sequence = ++request.current;
    setCatalog(undefined); setError(''); setComparison(''); setLoading(true);
    try {
      const next = await read<PermissionCatalogData>(`/api/sessions/${encodeURIComponent(sessionId)}/permissions/catalog`, {});
      if (sequence !== request.current) return;
      const before = previous.current;
      const sameQuery = before?.native_id === next.native_id && before.query_id === next.query_id && before.configuration_id === next.configuration_id;
      setComparison(sameQuery ? before.sample_hash === next.sample_hash ? '与本页上次读取一致；仅比较这两次报告。' : '权限规则报告已变化；请核对来源、忽略标记与实际动作。'
        : before ? '原生查询或配置已变化，已建立新的比较起点。' : '本次查询的首次读取。');
      previous.current = next;
      setCatalog(next); setSource(''); setLimit(40);
    } catch (caught) {
      if (sequence !== request.current) return;
      previous.current = undefined;
      setError((caught as Error).message);
    } finally {
      if (sequence === request.current) setLoading(false);
    }
  }
  const filtered = catalog?.items.filter(item => !source || item.source === source) ?? [];
  const sourceOptions = [...new Set(catalog?.items.map(item => item.source) ?? [])];

  return <details className="skill-discovery permission-catalog"><summary>Claude 权限规则与来源</summary>
    <p className="hint">查看原生查询报告的允许、拒绝和询问规则，以及被受管策略忽略的声明。实际动作还受权限模式、hooks 和逐次审批影响。</p>
    <button disabled={!connected || loading} onClick={() => void sample()}>{loading ? '正在读取权限规则…' : '读取当前权限规则'}</button>
    {!connected ? <p className="hint">工作台已断开，连接后可重新读取。</p> : null}
    {error ? <p className="error" role="alert">{error}</p> : null}
    {catalog ? <>
      <p role="status">当前查询报告 {catalog.items.length} 条规则 · 完整权限仍需核对</p>
      <p className="mono">原生会话：{catalog.native_id}</p>
      <small>读取时间：{new Date(catalog.at).toLocaleString()}</small>
      <p>{comparison}</p>
      <p>{catalog.managed_only ? '原生报告：仅采用受管设置中的权限规则。' : '原生报告：允许合并非受管设置中的权限规则。'}</p>
      {catalog.conflict_count > 0 ? <p className="warning" role="alert">{catalog.conflict_count} 项规则同时出现不同的忽略标记，当前生效状态存在冲突。</p> : null}
      {catalog.errors.length > 0 ? <div className="error"><p>原生报告 {catalog.errors.length} 项配置解析错误；被跳过来源的规则可能缺席。</p>
        {catalog.errors.map((item, index) => <p className="mono" key={index}>{visible(item.file)}{item.field ? ` · ${visible(item.field)}` : ''}</p>)}</div> : null}
      <label>按权限来源查看<select value={source} onChange={event => { setSource(event.target.value); setLimit(40); }}>
        <option value="">全部来源</option>{sourceOptions.map(value => <option key={value} value={value}>{sourceName(value)}</option>)}
      </select></label>
      {filtered.slice(0, limit).map((item, index) => <div className="snapshot-asset" key={`${item.source}/${index}`}>
        <p>{behaviors[item.behavior] ?? visible(item.behavior)} · {sourceName(item.source)}</p>
        <p className="mono">{visible(item.rule)}</p>
        <small>{locations[item.editability] ?? visible(item.editability)} · {item.not_in_effect ? '原生标记：不生效' : '原生未标记忽略'}</small>
        {item.conflicting ? <p className="warning">同一来源、规则和行为的忽略标记不一致；保留全部原生条目。</p> : null}
      </div>)}
      {filtered.length > limit ? <button onClick={() => setLimit(value => value + 40)}>再显示 40 条（剩余 {filtered.length - limit} 条）</button> : null}
      {!catalog.items.length ? <p className="hint">本次报告没有列出规则；空清单不代表所有动作均被允许。</p> : null}
      {catalog.directories.length > 0 ? <details><summary>原生报告的额外工作目录（{catalog.directories.length}）</summary>
        <p className="hint">以下为原生返回的来源标签，不能据此定位实际配置文件。</p>
        {catalog.directories.map((item, index) => <p key={index}><span className="mono">{visible(item.path)}</span> · 原生标签：{sourceName(item.source)}</p>)}</details> : null}
      <details><summary>读取范围与限制</summary>{catalog.limitations.map(text => <p className="hint" key={text}>{text}</p>)}</details>
    </> : null}
  </details>;
}
