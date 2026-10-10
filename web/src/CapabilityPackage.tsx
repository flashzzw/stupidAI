import { useEffect, useRef, useState, type FormEvent } from 'react';
import { CapabilityDiagnosis, type Diagnosis } from './CapabilityDiagnosis';

type Read = <T>(path: string, body?: unknown) => Promise<T>;
type Target = { id: string; name: string; revision: string };
type ExportRequest = { asset_id: string; revision: string; version: string; preview_hash?: string };
type ImportRequest = { archive: string; asset_id: string; action: string; base_revision: string; preview_hash?: string };
type Preview = {
  asset: { id: string; name: string; kind: string; version: string; backends: string[] };
  files: { path: string; kind: string; mode: number; sha256?: string }[]; bytes: number;
  diagnosis: Diagnosis; preview_hash: string; archive_sha256?: string;
  uses: { session_id: string; snapshot_id: string; title: string; version: string; phase: string }[];
};

export function CapabilityPackage({ exporting, targets, read, connected, close, saved }: {
  exporting?: ExportRequest; targets: Target[]; read: Read; connected: boolean; close: () => void; saved: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [preview, setPreview] = useState<Preview>();
  const [request, setRequest] = useState<ImportRequest>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [action, setAction] = useState('register');
  const title = exporting ? '导出资源包' : '导入资源包';
  useEffect(() => { dialog.current?.showModal(); }, []);
  async function inspect(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true); setError(''); setNotice(''); setPreview(undefined);
    try {
      if (exporting) {
        setPreview(await read<Preview>('/api/capabilities/package/export/preview', exporting));
      } else {
        const form = new FormData(event.currentTarget), file = form.get('archive');
        if (!(file instanceof File) || !file.size || file.size > 40 * 1024 * 1024) throw new Error('请选择不超过 40 MiB 的 ZIP 资源包。');
        const archive = await new Promise<string>((resolve, reject) => {
          const reader = new FileReader();
          reader.onload = () => resolve(String(reader.result).split(',')[1]);
          reader.onerror = () => reject(new Error('资源包文件读取失败。'));
          reader.readAsDataURL(file);
        });
        const id = String(form.get('asset_id') ?? '').trim();
        const next: ImportRequest = { archive, action, asset_id: id, base_revision: action === 'update' ? targets.find(target => target.id === id)?.revision ?? '' : '' };
        const value = await read<Preview>('/api/capabilities/package/import/preview', next);
        setRequest(next); setPreview(value);
      }
    } catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  }
  async function apply() {
    if (!preview) return;
    setBusy(true); setError(''); setNotice('');
    try {
      if (exporting) {
        const response = await fetch('/api/capabilities/package/export', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ...exporting, preview_hash: preview.preview_hash }) });
        if (!response.ok) { const value = await response.json(); throw new Error(value.error ?? '导出失败'); }
        const blob = await response.blob(), url = URL.createObjectURL(blob);
        const link = document.createElement('a'); link.href = url; link.download = `${preview.asset.id}.zip`;
        document.body.append(link); link.click(); link.remove();
        window.setTimeout(() => URL.revokeObjectURL(url), 1000);
        setNotice('资源包已生成并交给浏览器下载。');
      } else if (request) {
        const key = 'netcap.capability-package-command.v1';
        // Retain retry identity without putting potentially large resource bytes in browser storage.
        const signature = JSON.stringify({ archive: preview.archive_sha256, id: preview.asset.id, action: request.action, revision: request.base_revision, preview: preview.preview_hash });
        const previous = JSON.parse(sessionStorage.getItem(key) ?? 'null') as { id: string; signature: string } | null;
        const id = previous?.signature === signature ? previous.id : crypto.randomUUID();
        sessionStorage.setItem(key, JSON.stringify({ id, signature }));
        await read('/api/capabilities/package/import', { command_id: id, kind: 'import_capability_package', capability_package: { ...request, preview_hash: preview.preview_hash } });
        sessionStorage.removeItem(key); saved();
      }
    } catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  }
  return <dialog ref={dialog} className="dialog capability-dialog" onClose={close} aria-labelledby="capability-package-title">
    <div className="section-heading"><h2 id="capability-package-title">{title}</h2><button onClick={close} disabled={busy} aria-label="关闭资源包">×</button></div>
    <p className="muted">ZIP 包含声明文件、脚本与权限。导入保存为独立固定版本；运行环境和原生安装需另行配置。</p>
    <form onSubmit={event => void inspect(event)} onChange={() => { setPreview(undefined); setRequest(undefined); setError(''); setNotice(''); }}>
      <fieldset disabled={busy}>
        {exporting ? <p>{exporting.asset_id} · {exporting.version}</p> : <>
          <label>ZIP 资源包<input type="file" name="archive" accept=".zip,application/zip" required /></label>
          <label>导入方式<select value={action} onChange={event => setAction(event.target.value)}><option value="register">登记新资产</option><option value="update">更新已有资产</option></select></label>
          {action === 'register' ? <label>目标资产 ID（可选）<input name="asset_id" pattern="(?:[A-Za-z0-9_]|-)+" maxLength={100} placeholder="留空沿用包内 ID" /></label> :
            <label>更新目标<select name="asset_id" required><option value="">请选择已有资产</option>{targets.map(target => <option key={target.id} value={target.id}>{target.name} · {target.id}</option>)}</select></label>}
        </>}
        <button disabled={!connected}>{busy ? '正在处理…' : '预览资源包'}</button>
      </fieldset>
    </form>
    {preview ? <section className="capability-preview" aria-label="资源包预览">
      <h3>{preview.asset.name} · {preview.asset.version}</h3><p>{preview.asset.id} · {preview.asset.backends.join(' / ')} · {(preview.bytes / 1024).toFixed(1)} KiB</p>
      <CapabilityDiagnosis value={preview.diagnosis} />
      <p className="hint">{preview.diagnosis.boundary}。仅识别已知凭据模式；共享前仍需检查文件内容。</p>
      <details className="package-files"><summary>资源清单 · {preview.files.length} 项</summary>
        {preview.files.map(file => <p key={file.path}><span className="mono">{file.path}</span> · {file.kind === 'directory' ? '目录' : '文件'} · 权限 {file.mode.toString(8)}
          {file.sha256 ? <small className="mono">{file.sha256}</small> : null}</p>)}
      </details>
      <p>已记录引用：{preview.uses.length} 处。已有快照保持原绑定。</p>
      {preview.uses.map(use => <p key={`${use.session_id}/${use.snapshot_id}/${use.phase}`}>{use.title} · {use.version} · {use.phase}</p>)}
      <button className="primary" disabled={!connected || busy || !preview.diagnosis.portable} onClick={() => void apply()}>{exporting ? '确认下载资源包' : '确认导入固定版本'}</button>
    </section> : null}
    {error ? <p className="error" role="alert">{error}</p> : null}
    {notice ? <p className="hint" role="status">{notice}</p> : null}
  </dialog>;
}
