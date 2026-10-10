// Built UI replay of real Handler states from successful native hook runs.
async page => {
  const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
  let phase='before-restart';
  const sid=fixture.sessions.A;
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  page.setDefaultTimeout(6000);await page.unrouteAll({behavior:'ignoreErrors'});
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{
    sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);
    window.__hookSuccessStreams=[];
    window.EventSource=class{constructor(){window.__hookSuccessStreams.push(this);setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};
  },sid);
  await page.route('**/*',async route=>{
    const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];
    requests.push({path,method:r.method()});
    if(fixture.responses[phase][path])return route.fulfill({json:fixture.responses[phase][path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:409,body:'No mutation in hook history replay'});
  });
  await page.setViewportSize({width:1440,height:1050});
  for(const next of ['before-restart','reopened','final']){
    phase=next;await page.goto('http://127.0.0.1:9463/');
    await page.locator('.execution-snapshot>summary').click();
    const panel=page.locator('.hook-executions');await panel.locator(':scope>summary').click();
    const expected=fixture.responses[phase]['/api/sessions/'+sid].session.view.hook_executions;
    const rows=panel.locator('.snapshot-asset');
    check(phase+'_exact_success_count',await rows.count()===expected.length);
    for(const hook of expected){
      const row=rows.filter({hasText:'关联操作：'+hook.operation});
      const text=await row.innerText();
      check(phase+'_'+hook.id+'_native_success',text.includes('userPromptSubmit · 成功结束')&&text.includes('原生状态：completed'));
      check(phase+'_'+hook.id+'_original_identity',text.includes(hook.native_run_id)&&text.includes(hook.turn_id));
      check(phase+'_'+hook.id+'_original_asset_version',text.includes('success-hooks · 资源 '+hook.asset_version)&&text.includes(hook.execution_snapshot_id)&&text.includes(hook.source_hash));
      check(phase+'_'+hook.id+'_source_time_duration',text.includes(hook.source_path)&&text.includes('开始：')&&text.includes('结束：')&&text.includes(hook.duration_ms+' 毫秒'));
    }
    const text=await panel.innerText();
    check(phase+'_private_output_and_exit_absent',!text.includes('PRIVATE_HOOK_OUTPUT')&&!text.includes('python3')&&!text.includes('原生退出码'));
    check(phase+'_old_and_new_versions_visible',text.includes('资源 asset-v1')&&text.includes('资源 asset-v2'));
    check(phase+'_no_false_timeout_or_running',!text.includes('hook 超时')&&!text.includes('userPromptSubmit · 执行中'));
  }
  await page.locator('.hook-executions').scrollIntoViewIfNeeded();
  await page.screenshot({path:'.dev/w0208-hook-success/success-1440.png',fullPage:true});
  for(const label of ['B','C']){
    await page.locator('.session-row').filter({hasText:'Successful hook '+label}).click();
    await page.getByRole('heading',{name:'Successful hook '+label,exact:true}).waitFor();
    check(label+'_has_no_other_session_hooks',await page.locator('.hook-executions').count()===0);
  }
  await page.locator('.session-row').filter({hasText:'Successful hook A'}).click();
  await page.locator('.execution-snapshot>summary').click();
  const panel=page.locator('.hook-executions');await panel.locator(':scope>summary').click();
  await page.evaluate(()=>window.__hookSuccessStreams.forEach(s=>s.onerror?.(new Event('error'))));
  await page.getByText('连接断开 · 数据已过期',{exact:true}).waitFor();
  check('offline_success_history_retained',await panel.getByText('userPromptSubmit · 成功结束',{exact:true}).count()===4);
  check('offline_current_connection_unverified',(await panel.innerText()).includes('连接尚未核对；保留已记录结果'));
  await page.setViewportSize({width:390,height:844});
  await page.getByRole('button',{name:'详情',exact:true}).click();await panel.scrollIntoViewIfNeeded();
  check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await page.screenshot({path:'.dev/w0208-hook-success/success-390.png',fullPage:true});
  check('only_read_requests',requests.every(r=>r.method==='GET'));
  check('no_page_errors',errors.length===0);
  return {checks,passed:Object.values(checks).every(Boolean),requests,errors,scope:'real Handler successful hook history before/after native restart, version attribution and simulated browser disconnection; no execution commands'};
}
