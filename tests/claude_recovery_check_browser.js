// Recovery reports and session data come from the real Application fixture.
async page=>{
  const fixture=__FIXTURE__,assets=__ASSETS__,requests=[],checks={},errors=[];
  const clone=v=>JSON.parse(JSON.stringify(v)),sid=fixture.session_id,detailPath='/api/sessions/'+sid,path=detailPath+'/execution/recovery-check';
  let mode='blocked',release;
  const check=(k,v)=>{checks[k]=!!v;if(!v)throw Error(k);};
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){this.listeners={};setTimeout(()=>this.onopen?.(),0);}addEventListener(k,f){this.listeners[k]=f;}close(){}};},sid);
  await page.route('**/*',async route=>{
    const r=route.request(),p=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];requests.push({path:p,method:r.method(),body:r.postData()?JSON.parse(r.postData()):undefined});
    if(p===path){
      if(mode==='error')return route.fulfill({status:409,json:{error:'fixture: 原操作或来源已变化，请重新核对'}});
      const out=clone(mode==='blocked'?fixture.blocked:fixture.passed);
      if(mode==='late')await new Promise(resolve=>release=resolve);
      if(mode==='wrong-operation')out.native.operation='other';
      if(mode==='unknown-version')out.native.version=2;
      if(mode==='not-read-only')out.native.read_only=false;
      if(mode==='missing-check')out.native.checks.pop();
      return route.fulfill({json:out});
    }
    if(fixture.responses[p])return route.fulfill({json:fixture.responses[p]});
    if(assets[p])return route.fulfill({path:assets[p],contentType:p.endsWith('.js')?'application/javascript':p.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'fixture'});
  });
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9455/');
  const snapshot=page.locator('.execution-snapshot');await snapshot.locator(':scope > summary').click();
  const panel=page.getByRole('region',{name:'恢复前只读核对'}),button=panel.getByRole('button',{name:'核对恢复条件',exact:true});
  check('no_automatic_probe',requests.every(r=>r.path!==path));await button.click();await panel.getByText('还有条件未满足，保持待核对。',{exact:true}).waitFor();
  check('missing_original_settings_visible',(await panel.innerText()).includes('原本地设置文件版本：未满足'));
  check('native_exit_is_independent',(await panel.innerText()).includes('自有查询已退出：已核对'));
  const changed=fixture.blocked.sources.find(s=>s.status!=='unchanged');check('changed_source_visible',!!changed&&(await panel.innerText()).includes(changed.path));
  const body=requests.find(r=>r.path===path).body,pending=fixture.responses[detailPath].session.pending_execution_change;
  check('exact_saved_request',body.base_snapshot_id===fixture.blocked.base_snapshot_id&&body.pending_change_id===fixture.blocked.pending_change_id&&body.operation_id===pending.operation_id&&Object.keys(body).length===3);
  await panel.screenshot({path:'.dev/w0208-recovery-check/blocked-1440.png'});
  mode='passed';await button.click();await panel.getByText('已列出的条件通过，仍保持暂停。',{exact:true}).waitFor();
  check('original_sources_match',(await panel.innerText()).includes('已记录的原配置来源均匹配。'));
  check('probe_does_not_resume',(await panel.innerText()).includes('不会启动查询、改变快照'));
  check('original_snapshot_unchanged',await snapshot.getByText(fixture.blocked.base_snapshot_id,{exact:true}).count()===1);
  check('candidate_not_promoted',await snapshot.getByText(`候选版本 ${pending.snapshot.revision} · 实际生效待核对`,{exact:true}).count()===1);
  check('input_stays_disabled',await page.getByRole('button',{name:'发送消息',exact:false}).isDisabled());
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await panel.scrollIntoViewIfNeeded();
  check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));await panel.screenshot({path:'.dev/w0208-recovery-check/passed-390.png'});await page.setViewportSize({width:1440,height:1100});
  for(const value of ['error','wrong-operation','unknown-version','not-read-only','missing-check']){
    mode=value;await button.click();await panel.getByRole('alert').waitFor();
    check(value+'_clears_success',await panel.getByText('已列出的条件通过，仍保持暂停。',{exact:true}).count()===0);
  }
  mode='late';await button.click();await page.waitForFunction(()=>[...document.querySelectorAll('button')].some(b=>b.textContent==='正在核对恢复条件…'));
  for(let n=0;n<100&&!release;n++)await page.waitForTimeout(10);if(!release)throw Error('held response did not start');
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));const response=page.waitForResponse(r=>r.url().endsWith('/execution/recovery-check'));release();
  await response;await page.waitForTimeout(50);
  check('late_reply_does_not_restore_success',await panel.getByRole('status').count()===0);
  check('offline_blocks_probe',await button.isDisabled());
  check('no_execution_commands',requests.every(r=>r.path!=='/api/commands'));
  check('no_page_errors',errors.length===0);
  return {checks,requests,errors,scope:'real Application recovery reports; HTTP/SSE and fault responses replayed; no native execution from browser'};
}
