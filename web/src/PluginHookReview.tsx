import { useEffect, useRef, useState } from 'react';
import type { CapabilityAsset } from './ExecutionDetails';

export type HookReview = { inspection_id: string; trust: 'trusted' | 'untrusted' };
type Read = <T>(path: string, body?: unknown) => Promise<T>;
type Definition = { key: string; path: string; event: string; definition_hash: string; background: boolean; timeout_seconds: number };
type Inspection = { inspection_id: string; asset_version: string; request: { asset_id: string; instance_id: string; revision: string; source_sha256: string };
  inspection: { scope: string; source_hash: string; definitions: Definition[] } };

// Mount with a key for asset/version/instance/connection. This keeps a review
// receipt from surviving a changed execution target or a disconnected form.
export function PluginHookReview({ asset, instanceId, disabled, read, onChange, formField }: {
  asset: CapabilityAsset; instanceId: string; disabled: boolean; read: Read;
  onChange?: (review: HookReview | undefined) => void; formField?: boolean;
}) {
  const [result, setResult] = useState<Inspection>();
  const [trusted, setTrusted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const sequence = useRef(0);
  useEffect(() => () => { sequence.current++; }, []);
  const review = result && !disabled ? { inspection_id: result.inspection_id, trust: trusted ? 'trusted' as const : 'untrusted' as const } : undefined;
  async function inspect() {
    const current = ++sequence.current;
    setBusy(true); setResult(undefined); setTrusted(false); setError(''); onChange?.(undefined);
    try {
      const rows = await read<{ asset: CapabilityAsset; revision: string; removed: boolean; source?: { sha256?: string } }[]>('/api/capabilities');
      const item = rows.find(row => row.asset.id === asset.id && row.asset.version === asset.version && !row.removed);
      if (!item?.source?.sha256) throw new Error('登记版本或来源已变化，请刷新后重新选择。');
      const request = { asset_id: asset.id, instance_id: instanceId, revision: item.revision, source_sha256: item.source.sha256 };
      const value = await read<Inspection>('/api/capabilities/codex-hooks', request);
      if (current !== sequence.current) return;
      if (!value.inspection_id || value.asset_version !== asset.version || value.request.asset_id !== asset.id ||
          value.request.instance_id !== instanceId || value.request.revision !== request.revision || value.request.source_sha256 !== request.source_sha256 ||
          value.inspection.scope !== 'package' || value.inspection.source_hash !== request.source_sha256 || !value.inspection.definitions.length) {
        throw new Error('原生审核结果与所选版本不匹配，请重新读取。');
      }
      if (value.inspection.definitions.some(hook => hook.background)) throw new Error('该包含异步 hook；当前选配尚未核对这类执行。');
      setResult(value); onChange?.({ inspection_id: value.inspection_id, trust: 'untrusted' });
    } catch (e) { if (current === sequence.current) setError((e as Error).message); }
    finally { if (current === sequence.current) setBusy(false); }
  }
  return <details className="plugin-hook-review"><summary>此版本的 hook 信任</summary>
    <p className="hint">含 hook 的插件需先核对完整包版本。启用整包不会自动信任其中的命令；模型的只读策略不能替代 hook 命令的执行信任。</p>
    <button type="button" disabled={disabled || busy || !instanceId} onClick={() => void inspect()}>{busy ? '正在核对…' : '读取 hook 审核依据'}</button>
    {result && !disabled ? <div>
      <p role="status">{asset.version} · {result.inspection.definitions.length} 项同步 hook 已读取</p>
      <p className="mono">{asset.source}</p><p className="mono">完整内容标识：{result.inspection.source_hash}</p>
      {result.inspection.definitions.map(hook => <p key={hook.key}>{hook.event} · {hook.path} · 期限 {hook.timeout_seconds} 秒</p>)}
      <label className="plugin-hook-trust"><input type="checkbox" checked={trusted} disabled={disabled || busy} onChange={event => {
        setTrusted(event.target.checked); onChange?.({ inspection_id: result.inspection_id, trust: event.target.checked ? 'trusted' : 'untrusted' });
      }} /><span>我已检查此完整版本，允许其同步 hook 命令在本会话执行</span></label>
      <p className="hint">{trusted ? '本次选择将信任该完整版本的 hook。' : '保持未信任：插件可加载，hook 命令不会获得本次执行信任。'}来源升级需重新审核；读取本身不会执行命令。</p>
    </div> : null}
    {formField && review ? <input type="hidden" name={`capability_hook_review:${asset.id}`} value={JSON.stringify(review)} /> : null}
    {error ? <p role="alert" className="error">{error}</p> : null}
  </details>;
}

export function CodexPluginChoice({ asset, instanceId, disabled, read }: { asset: CapabilityAsset; instanceId: string; disabled: boolean; read: Read }) {
  const [mode, setMode] = useState('inherit');
  return <div><label className="native-capability-selection">{asset.name} · CLI 插件选择
    <select name={`capability_selection:${asset.id}`} value={mode} disabled={disabled || asset.disabled} onChange={event => setMode(event.target.value)}>
      <option value="inherit">继承原生配置</option><option value="enabled">本次启用整包</option><option value="disabled">本次禁用整包</option>
    </select></label>
    <small>插件使用独立版本副本。含同步 hook 的旧格式插件需审核版本；异步及通用格式 hook 暂不支持选配。</small>
    {mode !== 'inherit' ? <PluginHookReview key={`${asset.id}/${asset.version}/${instanceId}/${disabled}`} asset={asset} instanceId={instanceId} disabled={disabled || Boolean(asset.disabled)} read={read} formField /> : null}
  </div>;
}
