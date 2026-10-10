// Authenticated claude_execution_apply_smoke.py --hold fixture.
async (page) => {
  const checks = {}, errors = [];
  const onError = e => errors.push(String(e));
  page.on('pageerror', onError);
  const check = (name, value) => { checks[name] = Boolean(value); if (!value) throw Error(name); };
  try {
    await page.setViewportSize({width:1440,height:1000});
    await page.getByRole('button',{name:'＋ 新建',exact:true}).click();
    await page.getByLabel('会话名称',{exact:true}).fill('Claude 原 ID 双服务改配');
    await page.locator('select[name="capability_selection:shared"]').selectOption('enabled');
    const created = page.waitForResponse(r=>r.url().endsWith('/api/commands')&&r.request().postDataJSON()?.kind==='create');
    await page.getByRole('button',{name:'创建会话',exact:true}).click();
    const sid=(await (await created).json()).session_id;
    const read=()=>page.evaluate(async id=>(await fetch(`/api/sessions/${id}/execution`)).json(),sid);
    const session=()=>page.evaluate(async id=>(await (await fetch('/api/snapshot')).json()).sessions.find(s=>s.id===id),sid);
    await page.waitForFunction(()=>document.querySelector('.badge')?.textContent==='空闲');
    const native=(await session()).scope.native_id, initial=(await read()).snapshot;
    await page.locator('.execution-snapshot summary').click();
    await page.getByText('identity · 已连接 · 与选择一致',{exact:true}).waitFor();
    const section=page.getByRole('region',{name:'原会话 MCP 改配'});
    check('one_choice_for_multi_server_asset',await section.locator('select[data-mcp-mode]').count()===1);
    for(const [index,mode] of ['disabled','enabled'].entries()) {
      const before=(await read()).snapshot;
      await section.locator('select[data-mcp-mode]').selectOption(mode);
      await section.getByRole('button',{name:'预览改配差异',exact:true}).click();
      await section.getByText(`候选版本 ${index+2} · 与当前生效快照比较`,{exact:true}).waitFor();
      const previewText=await section.innerText();
      check(mode+'_preview_covers_both_servers',previewText.includes('identity：')&&previewText.includes('companion：'));
      check(mode+'_explains_owned_query_restart',previewText.includes('关闭已确认空闲的自有 Claude 查询'));
      await section.getByRole('button',{name:'保存待应用选择',exact:true}).click();
      await section.getByText(`待应用版本 ${index+2} · 尚未生效`,{exact:true}).waitFor();
      check(mode+'_save_keeps_current_version',JSON.stringify((await read()).snapshot)===JSON.stringify(before));
      await section.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).click();
      await section.getByText('已取得空闲依据；明确应用将关闭此查询并按原 ID 加载新版本。',{exact:true}).waitFor();
      check(mode+'_prepare_holds_and_pauses',(await read()).hold&&(await session()).paused);
      const applying=page.waitForResponse(r=>r.url().endsWith('/api/commands')&&r.request().postDataJSON()?.kind==='apply_execution');
      await section.getByRole('button',{name:'应用已保存配置',exact:true}).click();
      const receipt=await (await applying).json();
      // The fixed CLI's waitForFunction treats an async predicate's Promise as
      // truthy. Await each API read explicitly before accepting the condition.
      const deadline=Date.now()+60000;
      while (true) {
        const ready=await page.evaluate(async args=>{
          const data=await (await fetch('/api/snapshot')).json();
          return data.operations.some(o=>o.id===args.operation&&o.status==='succeeded')&&data.sessions.find(s=>s.id===args.sid)?.execution_snapshot.revision===args.revision;
        },{operation:receipt.operation_id,sid,revision:index+2});
        if(ready)break;
        if(Date.now()>deadline)throw Error('configuration did not settle');
        await page.waitForTimeout(100);
      }
      const after=await read(), actual=await session();
      check(mode+'_same_native_id',actual.scope.native_id===native);
      const selected=after.capability_evidence.filter(e=>e.asset_id==='shared');
      if(selected.length!==2||!selected.every(e=>e.mode===mode&&e.verified)) throw Error(mode+'_all_servers_match '+JSON.stringify({readRevision:after.snapshot.revision,selected,actualRevision:actual.execution_snapshot.revision,actualEvidence:actual.view.capability_evidence}));
      check(mode+'_all_servers_match',true);
      check(mode+'_history_and_pause_preserved',after.history.length===index+1&&actual.paused&&!after.hold);
      check(mode+'_single_asset_control_after_promotion',await section.locator('select[data-mcp-mode]').count()===1);
    }
    check('first_snapshot_kept_exactly',JSON.stringify((await read()).history[0])===JSON.stringify(initial));
    await page.getByRole('textbox',{name:'消息',exact:true}).fill('NETCAP_A_TURN_1');
    await page.getByRole('button',{name:'发送消息',exact:true}).click();
    await page.getByRole('button',{name:'允许本次',exact:true}).waitFor();
    check('new_version_still_requires_single_call_permission',await page.getByRole('button',{name:'允许此会话',exact:true}).count()===0);
    await page.getByRole('button',{name:'允许本次',exact:true}).click();
    await page.waitForFunction(()=>document.querySelector('.badge')?.textContent==='空闲');
    await page.getByText('历史成功调用：whoami',{exact:true}).waitFor();
    for(const width of [1440,768]) {
      await page.setViewportSize({width,height:1000});
      if(width===768)await page.getByRole('button',{name:'详情',exact:true}).click();
      await section.scrollIntoViewIfNeeded();
      check('layout_'+width,await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
      await page.screenshot({path:`.dev/w0206-apply/browser-${width}.png`});
    }
    check('no_javascript_errors',errors.length===0);
    return {checks,errors,expectations_met:Object.values(checks).every(Boolean),session:sid,native_id:native};
  } finally {page.off('pageerror',onError);}
}
