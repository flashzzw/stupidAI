// Real Application failure/read payloads; HTTP/SSE are replayed, no execution commands.
async page=>{
  const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
  const sid=fixture.session_id,detailPath='/api/sessions/'+sid,base=fixture.responses[detailPath];
  const clone=v=>JSON.parse(JSON.stringify(v)),expected=base.session.view.configuration_failure;
  let sequence=1000,override={},emptyBinding=false;
  const current=()=>{const s=clone(base.session);Object.assign(s.view.configuration_failure,override);if(emptyBinding){s.pending_execution_change.operation_id='';s.pending_execution_change.baseline='';s.view.configuration_failure.operation='';s.view.configuration_failure.baseline='';}return s;};
  const check=(name,value)=>{checks[name]=Boolean(value);if(!value)throw Error(name);};
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){this.listeners={};window.failureEvents=this;setTimeout(()=>this.onopen?.(),0);}addEventListener(k,f){this.listeners[k]=f;}close(){}};},sid);
  await page.route('**/*',async route=>{
    const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];requests.push({path,method:r.method()});
    if(path===detailPath)return route.fulfill({json:{...clone(base),session:current(),sequence}});
    if(path==='/api/workspace'||path==='/api/snapshot')return route.fulfill({json:{...clone(fixture.responses[path]),sessions:[current()],sequence}});
    if(fixture.responses[path])return route.fulfill({json:fixture.responses[path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'fixture'});
  });
  const refresh=async()=>{sequence+=100;await page.evaluate(()=>window.failureEvents.listeners.change());};
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9455/');
  const snapshot=page.locator('.execution-snapshot');await snapshot.locator(':scope > summary').click();
  const panel=page.getByRole('region',{name:'原会话配置改配'}),record=panel.getByRole('region',{name:'配置应用失败记录'});
  await record.waitFor();
  check('fixed_reason_visible',(await record.innerText()).includes(expected.detail));
  check('operation_bound',(await record.innerText()).includes(expected.operation));
  check('original_snapshot_retained',await snapshot.getByText(base.session.execution_snapshot.id,{exact:true}).count()===1);
  check('candidate_stays_unverified',(await panel.innerText()).includes('实际生效待核对'));
  check('cleanup_is_not_rollback',(await record.innerText()).includes('本次自有查询已确认退出；已发生的副作用不会因此撤销。'));
  check('no_prepare_or_apply',await panel.getByRole('button',{name:'应用已保存配置',exact:true}).count()===0&&await panel.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).count()===0);
  check('no_discard_unknown',await panel.getByRole('button',{name:'撤销待应用选择',exact:true}).isDisabled());
  check('no_input',await page.getByRole('button',{name:'发送消息',exact:false}).isDisabled());
  check('offline_guard_not_registered',(await page.getByRole('region',{name:'本地设置自动应用限制'}).innerText()).includes('当前未核对'));
  await record.screenshot({path:'.dev/w0208-configuration-failure/failure-1440.png'});
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await record.scrollIntoViewIfNeeded();
  check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));await record.screenshot({path:'.dev/w0208-configuration-failure/failure-390.png'});
  await page.setViewportSize({width:1440,height:1100});
  for(const [name,value] of [['operation','other'],['baseline','other'],['version',2],['stage','unknown'],['reason','unknown'],['cleanup','probably']]){
    override={[name]:value};await refresh();await record.waitFor({state:'hidden'});check(name+'_mismatch_hidden',await record.count()===0);
    override={};await refresh();await record.waitFor();
  }
  emptyBinding=true;await refresh();await record.waitFor({state:'hidden'});check('empty_binding_hidden',await record.count()===0);emptyBinding=false;await refresh();await record.waitFor();
  override={cleanup:'unconfirmed'};await refresh();await record.getByText('自有查询退出尚未确认；可能仍有执行或副作用。',{exact:true}).waitFor();
  check('unconfirmed_exit_explicit',(await record.innerText()).includes('退出尚未确认'));
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));await record.getByText('连接已断开，以下为已保存记录；当前执行状态仍待核对。',{exact:true}).waitFor();
  check('disconnect_keeps_historical_record',(await record.innerText()).includes(expected.operation));
  check('disconnect_cannot_reconcile',await page.getByRole('button',{name:'重新核对',exact:true}).isDisabled());
  check('zero_execution_commands',requests.every(r=>r.path!=='/api/commands'));
  check('no_page_errors',errors.length===0);
  return {checks,requests,errors,scope:'actual Application failure payload; HTTP/SSE and invalid evidence transitions replayed; no native execution from browser'};
}
