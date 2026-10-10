// Authenticated claude_execution_resources_smoke.py --hold fixture.
async page => {
  const checks={}, errors=[];
  const check=(name,value)=>{checks[name]=Boolean(value);if(!value)throw Error(name);};
  const onError=e=>errors.push(String(e)); page.on('pageerror',onError);
  const api=(path,body)=>page.evaluate(async ({path,body})=>{
    const r=await fetch(path,{method:body?'POST':'GET',headers:{'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});
    const result=await r.json();if(!r.ok)throw Error(JSON.stringify(result));return result;
  },{path,body});
  const until=async predicate=>{const deadline=Date.now()+60000;while(!await predicate()){if(Date.now()>deadline)throw Error('state did not settle');await page.waitForTimeout(100);}};
  try {
    await page.setViewportSize({width:1440,height:1100});
    await page.getByRole('button',{name:'＋ 新建',exact:true}).click();
    await page.getByLabel('会话名称',{exact:true}).fill('Claude 资源升级与继承');
    const creating=page.waitForResponse(r=>r.url().endsWith('/api/commands')&&r.request().postDataJSON()?.kind==='create');
    await page.getByRole('button',{name:'创建会话',exact:true}).click();
    const sid=(await (await creating).json()).session_id;
    const read=()=>api(`/api/sessions/${sid}/execution`);
    const session=async ()=>(await api('/api/snapshot')).sessions.find(s=>s.id===sid);
    await until(async ()=>(await session()).status==='idle');
    const native=(await session()).scope.native_id, initial=(await read()).snapshot;
    await page.locator('.execution-snapshot > summary').click();
    const section=page.getByRole('region',{name:'原会话能力改配'});
    check('empty_session_can_add_assets',await section.getByLabel('添加 MCP 资产',{exact:true}).count()===1);
    await section.getByLabel('添加 MCP 资产',{exact:true}).selectOption('shared');
    await section.getByLabel('添加 MCP 资产',{exact:true}).selectOption('additional');
    check('one_mode_control_per_asset',await section.locator('[data-mcp-mode]').count()===2);
    const apply=async (name,revision)=>{
      const before=(await read()).snapshot;
      await section.getByRole('button',{name:'预览改配差异',exact:true}).click();
      await section.getByText(`候选版本 ${revision} · 与当前生效快照比较`,{exact:true}).waitFor();
      const text=await section.innerText();
      if(name==='upgrade')check('resource_versions_in_preview',text.includes('资源 v1 → v2')&&text.includes('companion：启用 → 继承')&&text.includes('successor：继承 → 启用'));
      if(name==='upgrade')await page.screenshot({path:'.dev/w0206-resources/browser-upgrade-preview-1440.png'});
      await section.getByRole('button',{name:'保存待应用选择',exact:true}).click();
      await until(async ()=>(await read()).pending_change?.snapshot.revision===revision);
      const staged=await read();
      check(name+'_save_keeps_original',JSON.stringify(staged.snapshot)===JSON.stringify(before));
      await section.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).click();
      await section.getByText('已取得空闲依据；明确应用将关闭此查询并按原 ID 加载新版本。',{exact:true}).waitFor();
      const applying=page.waitForResponse(r=>r.url().endsWith('/api/commands')&&r.request().postDataJSON()?.kind==='apply_execution');
      await section.getByRole('button',{name:'应用已保存配置',exact:true}).click();
      const receipt=await (await applying).json();
      await until(async ()=>{const state=await api('/api/snapshot');return state.operations.some(o=>o.id===receipt.operation_id&&o.status==='succeeded')&&state.sessions.find(s=>s.id===sid)?.execution_snapshot.revision===revision;});
      await section.getByRole('button',{name:'撤销待应用选择',exact:true}).waitFor({state:'hidden'});
      const after=await read(), actual=await session();
      check(name+'_exact_saved_version',JSON.stringify(after.snapshot)===JSON.stringify(staged.pending_change.snapshot));
      check(name+'_same_id_paused_history',actual.scope.native_id===native&&actual.paused&&after.history.length===revision-1);
      return after;
    };
    const added=await apply('add',2);
    check('four_services_loaded',added.capability_evidence.filter(e=>e.asset_id&&e.verified&&e.runtime_status==='connected').length===4);
    const catalog=await api('/api/capabilities'), entry=catalog.find(x=>x.asset.id==='shared');
    const update={action:'update',asset_id:'shared',base_revision:entry.revision,asset:{...entry.asset,version:'v2',source:entry.asset.source.replace(/\/shared-mcp$/,'/shared-v2.json'),management:'managed'}};
    const preview=await api('/api/capabilities/preview',update);
    await api('/api/commands',{command_id:'browser-publish-v2',kind:'manage_capability',capability_change:{...preview.request,preview_hash:preview.preview_hash}});
    await section.locator('[data-mcp-version="shared"]').waitFor();
    check('library_update_keeps_pinned_ui_version',await section.locator('[data-mcp-version="shared"]').inputValue()==='v1');
    await section.locator('[data-mcp-version="shared"]').selectOption('v2');
    const upgraded=await apply('upgrade',3);
    check('upgraded_resource_and_other_pin',upgraded.snapshot.capability_refs.find(r=>r.asset.id==='shared').asset.version==='v2'&&upgraded.snapshot.capability_refs.find(r=>r.asset.id==='additional').asset.version==='r1');
    await page.screenshot({path:'.dev/w0206-resources/browser-upgrade-1440.png'});
    await section.locator('[data-mcp-mode="shared"]').selectOption('inherit');
    await section.locator('[data-mcp-mode="additional"]').selectOption('inherit');
    const restored=await apply('inherit',4);
    check('restored_has_no_explicit_dependencies',!restored.snapshot.mcp_selections?.length&&restored.snapshot.capability_refs.length===0);
    check('inherited_source_evidence',restored.capability_evidence.some(e=>e.server==='identity'&&e.mode==='inherit'&&e.runtime_status==='connected'&&e.detail.includes('project')));
    await page.getByText('继承的 MCP',{exact:true}).waitFor();
    check('empty_selection_still_allows_next_add',await section.getByLabel('添加 MCP 资产',{exact:true}).count()===1);
    check('original_snapshot_kept',JSON.stringify(restored.history[0])===JSON.stringify(initial));
    await page.getByRole('textbox',{name:'消息',exact:true}).fill('NETCAP_A_TURN_1_TOOL_identity');
    await page.getByRole('button',{name:'发送消息',exact:true}).click();
    await page.getByRole('button',{name:'允许本次',exact:true}).waitFor();
    check('inherited_permission_rule_preserved',await page.getByRole('button',{name:'允许此会话',exact:true}).count()===0);
    await page.getByRole('button',{name:'允许本次',exact:true}).click();
    await until(async ()=>(await session()).status==='idle');
    await page.getByText('历史成功调用：whoami',{exact:true}).waitFor();
    for(const width of [1440,768]){
      await page.setViewportSize({width,height:1100});
      if(width===768)await page.getByRole('button',{name:'详情',exact:true}).click();
      await section.scrollIntoViewIfNeeded();
      check('layout_'+width,await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
      await page.screenshot({path:`.dev/w0206-resources/browser-inherit-${width}.png`});
    }
    check('no_javascript_errors',errors.length===0);
    return {checks,errors,expectations_met:Object.values(checks).every(Boolean),session:sid,native_id:native};
  } finally {page.off('pageerror',onError);}
}
