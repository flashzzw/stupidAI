import { useEffect, useRef, useState } from 'react';

type Instance = { id: string; name?: string; kind: string };
type Read = <T>(path: string, body?: unknown) => Promise<T>;
type Request = { asset_id: string; revision: string; source_sha256: string; instance_id: string };
type Definition = { key: string; path: string; event: string; type: string; matcher?: string;
  definition_hash: string; timeout_seconds: number; display_order: number; background: boolean };
type Inspection = { request: Request; asset_version: string; inspection: { scope: string; source: string;
  instance_id: string; server_version: string; source_hash: string; native_source: string; at: string; definitions: Definition[] } };

export function PluginHookInspection({ assetId, version, revision, sourceHash, instances, connected, read }: {
  assetId: string; version: string; revision: string; sourceHash: string; instances: Instance[]; connected: boolean; read: Read;
}) {
  const available = instances.filter(instance => instance.kind === 'codex');
  const [instanceId, setInstanceId] = useState(available[0]?.id ?? '');
  const [result, setResult] = useState<Inspection>();
  const [resultKey, setResultKey] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const sequence = useRef(0);
  const instanceKey = JSON.stringify(instances);
  const bindingKey = JSON.stringify([assetId, version, revision, sourceHash, instanceId, instanceKey]);
  useEffect(() => {
    sequence.current++; setResult(undefined); setError(''); setBusy(false);
    return () => { sequence.current++; };
  }, [assetId, version, revision, sourceHash, instanceId, instanceKey, connected]);
  const validInstance = available.some(instance => instance.id === instanceId);
  async function inspect() {
    if (!connected || !validInstance || !sourceHash || busy) return;
    const request: Request = { asset_id: assetId, revision, source_sha256: sourceHash, instance_id: instanceId };
    const current = ++sequence.current;
    setBusy(true); setResult(undefined); setError('');
    try {
      const value = await read<Inspection>('/api/capabilities/codex-hooks', request);
      if (current !== sequence.current) return;
      if (value?.asset_version !== version || value.request?.asset_id !== assetId || value.request.revision !== revision ||
          value.request.source_sha256 !== sourceHash || value.request.instance_id !== instanceId ||
          value.inspection?.scope !== 'package' || value.inspection.source !== 'codex.isolated.hooks/list' ||
          value.inspection.instance_id !== instanceId || value.inspection.source_hash !== sourceHash ||
          value.inspection.server_version !== '0.158.0' || !Array.isArray(value.inspection.definitions) || !value.inspection.definitions.length) {
        throw new Error('返回的 hook 定义与本次包版本或实例不一致，请重新读取。');
      }
      setResultKey(bindingKey);
      setResult(value);
    } catch (e) {
      if (current === sequence.current) setError((e as Error).message);
    } finally {
      if (current === sequence.current) setBusy(false);
    }
  }
  return <details className="skill-discovery plugin-hook-inspection"><summary>Codex 插件 hook 定义</summary>
    <p className="hint">使用所选 Codex 实例的版本核对当前包定义。授信、会话选配与实际执行分别记录。</p>
    <label>读取实例<select value={instanceId} disabled={!connected} onChange={event => setInstanceId(event.target.value)}>
      <option value="">选择 Codex 实例</option>
      {available.map(instance => <option key={instance.id} value={instance.id}>{instance.name || instance.id}</option>)}
    </select></label>
    <button disabled={!connected || !validInstance || !sourceHash || busy} onClick={() => void inspect()}>{busy ? '正在核对包定义…' : '读取原生 hook 定义'}</button>
    {!sourceHash ? <p className="hint">请先刷新并核对当前资源来源。</p> : null}
    {!available.length ? <p className="hint">尚未登记 Codex 实例。</p> : null}
    {error ? <p className="error" role="alert">{error}</p> : null}
    {result && resultKey === bindingKey && connected ? <div>
      <p role="status">包定义已核对 · {version} · {result.inspection.definitions.length} 个 handler</p>
      <p className="hint">原生版本 {result.inspection.server_version} · {new Date(result.inspection.at).toLocaleString()}。此结果不表示已授信、已选配或执行成功。</p>
      {result.inspection.definitions.map(hook => <div className="snapshot-asset" key={hook.key}>
        <strong>{hook.event}</strong><span className="badge">{hook.background ? '异步声明' : '同步声明'}</span>
        <p className="mono">包内入口：{hook.path}</p>
        <small>{hook.type} · 期限 {hook.timeout_seconds} 秒{hook.matcher ? ` · 匹配 ${hook.matcher}` : ''}</small>
        <details><summary>原生身份与定义摘要</summary><p className="mono">{hook.key}</p><p className="mono">{hook.definition_hash}</p></details>
      </div>)}
      <p className="hint">原生摘要不包含脚本正文；完整包版本仍以能力库内容标识核对。本次独立读取不会改变原会话的信任状态。</p>
    </div> : null}
  </details>;
}
