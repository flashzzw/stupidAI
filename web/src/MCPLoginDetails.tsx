import { useState } from 'react';
import type { ExecutionSnapshot } from './ExecutionDetails';

type Plan = { snapshot_id:string; server:string; asset_id:string; version:string; profile:string; directory:string; command:string; created_at:string };

export function MCPLoginDetails({sessionId,snapshot,pending,connected,read}:{
  sessionId:string; snapshot:ExecutionSnapshot; pending?:ExecutionSnapshot; connected:boolean;
  read:<T>(path:string,body?:unknown)=>Promise<T>;
}) {
  const options=[snapshot,...(pending?[pending]:[])].flatMap((target,index)=>(target.mcp_selections??[]).map(s=>({
    key:`${target.id}/${s.server}`,snapshot_id:target.id,server:s.server,label:`${index?'候选':'当前'}快照 · ${s.server}`,
  })));
  const [selected,setSelected]=useState(options[0]?.key??'');
  const [plan,setPlan]=useState<Plan>();
  const [busy,setBusy]=useState(false),[error,setError]=useState(''),[copied,setCopied]=useState(false);
  const choice=options.find(o=>o.key===selected)??options[0];
  if(!options.length)return null;
  async function prepare(){
    if(!choice)return;setBusy(true);setError('');setCopied(false);setPlan(undefined);
    try{
      // A lost response must retry the durable receipt, including after reload.
      // Keep each target separate so switching profiles or snapshots cannot reuse its ID.
      const key=`netcap.mcp-login.v1:${JSON.stringify([sessionId,choice.snapshot_id,choice.server])}`;
      const id=sessionStorage.getItem(key)??crypto.randomUUID();
      sessionStorage.setItem(key,id);
      const receipt=await read<{mcp_login:Plan}>('/api/commands',{command_id:id,kind:'prepare_mcp_login',session_id:sessionId,mcp_login:{snapshot_id:choice.snapshot_id,server:choice.server}});
      if(sessionStorage.getItem(key)===id)sessionStorage.removeItem(key);
      setPlan(receipt.mcp_login);
    }catch(e){setError((e as Error).message);}finally{setBusy(false);}
  }
  async function recover(){
    if(!choice)return;setBusy(true);setError('');setCopied(false);setPlan(undefined);
    try{
      const value=await read<{mcp_logins?:Plan[]}>(`/api/sessions/${encodeURIComponent(sessionId)}/execution`);
      const found=value.mcp_logins?.filter(p=>p.snapshot_id===choice.snapshot_id&&p.server===choice.server).sort((a,b)=>b.created_at.localeCompare(a.created_at))[0];
      setPlan(found);if(!found)setError('此快照和服务尚未准备认证命令。');
    }catch(e){setError((e as Error).message);}finally{setBusy(false);}
  }
  return <details className="mcp-login"><summary>原生 OAuth 认证</summary>
    <p className="hint">为 HTTP/SSE MCP 准备独立登录目录。准备不连接服务、不改变原会话；在该执行配置的终端环境中运行命令并完成浏览器授权。令牌由原生 CLI 保管，工具调用仍需审批。</p>
    <label>认证目标<select aria-label="OAuth 认证目标" value={choice?.key??''} disabled={busy} onChange={e=>{setSelected(e.target.value);setPlan(undefined);setError('');setCopied(false);}}>
      {options.map(o=><option key={o.key} value={o.key}>{o.label}</option>)}
    </select></label>
    <button disabled={!connected||busy} onClick={()=>void prepare()}>{busy?'正在处理…':'准备认证命令'}</button>
    <button disabled={!connected||busy} onClick={()=>void recover()}>查看已准备命令</button>
    {plan?<div role="status"><p>{plan.server} · 资源 {plan.version}</p><p className="hint">原生状态目录</p><p className="mono">{plan.profile}</p>
      <pre>{plan.command}</pre><button onClick={()=>void navigator.clipboard.writeText(plan.command).then(()=>setCopied(true)).catch(()=>setError('复制失败，请手动选择命令。'))}>{copied?'已复制':'复制认证命令'}</button>
      <p className="hint">命令保留原配置与管理策略。登录完成后再连接；若原查询已因认证失败退出，重启控制台后仍按原 ID 恢复。不要把授权回调或令牌粘贴到会话消息中。</p>
    </div>:null}
    {error?<p role="alert" className="error">{error}</p>:null}
  </details>;
}
