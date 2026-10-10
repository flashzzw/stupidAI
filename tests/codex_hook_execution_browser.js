// Real Application/Handler failure record; controlled transport and explicit
// synthetic unfinished records for display/connection edge cases.
async page => {
  page.setDefaultTimeout(4000);await page.unrouteAll({behavior:'ignoreErrors'});
  const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
  const sid=fixture.sessions[0];let synthetic='';
  const native=fixture.responses['/api/sessions/'+sid].session.view.hook_executions[0];
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  page.__codexHookExecutions={checks,requests,errors};page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{sessionStorage.setItem('netcap.selected.v1',id);window.__hookStreams=[];window.EventSource=class{constructor(){window.__hookStreams.push(this);setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};},sid);
  await page.route('**/*',async route=>{
    const request=route.request(),path=request.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];requests.push({path,method:request.method()});
    if(fixture.responses[path]) {
      const response=JSON.parse(JSON.stringify(fixture.responses[path]));
      if(synthetic&&response.session?.id===sid) {
        const hook=response.session.view.hook_executions[0];hook.status=synthetic;hook.native_status='running';delete hook.finished_at;delete hook.duration_ms;delete hook.failure_kind;
        response.session.status=synthetic;response.session.view.executions=[{id:hook.id,kind:'hook',status:synthetic}];
      }
      return route.fulfill({json:response});
    }
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'unmapped fixture'});
  });
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9463/');
  await page.locator('.execution-snapshot>summary').click();const panel=page.locator('.hook-executions');await panel.locator('summary').click();
  let text=await panel.innerText();
  check('native_failure_visible',text.includes('sessionStart · 失败结束'));
  check('safe_timeout_visible',text.includes('原生报告：hook 超时'));
  check('native_status_preserved',text.includes('原生状态：failed'));
  check('native_run_id_visible',text.includes(native.native_run_id));
  check('original_turn_visible',text.includes(native.turn_id));
  check('original_operation_visible',text.includes(native.operation));
  check('cache_source_visible',text.includes(native.source_path));
  check('source_not_guessed_as_plugin_id',text.includes('没有完整插件 ID'));
  check('no_invented_exit_code',!text.includes('原生退出码'));
  check('duration_visible',text.includes(String(native.duration_ms)+' 毫秒'));
  await page.screenshot({path:'.dev/w0208-codex-execution/failure-1440.png',fullPage:true});
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await panel.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.screenshot({path:'.dev/w0208-codex-execution/failure-390.png',fullPage:true});
  await page.setViewportSize({width:1440,height:1100});await page.locator('.session-row').filter({hasText:'No hook invocation'}).click();
  check('other_session_does_not_inherit_history',await panel.count()===0);
  await page.locator('.session-row').filter({hasText:'Codex hook failure'}).click();await page.locator('.execution-snapshot>summary').click();await panel.locator('summary').click();
  check('return_keeps_recorded_failure',(await panel.innerText()).includes(native.operation));
  for(const [status,label] of [['running','执行中'],['unknown','终态待核对']]) {
    synthetic=status;await page.reload();await page.locator('.execution-snapshot>summary').click();await panel.locator('summary').click();
    text=await panel.innerText();check(status+'_visible',text.includes('sessionStart · '+label));
    check(status+'_no_terminal_invented',!text.includes('结束：')&&!text.includes('原生报告：hook 超时'));
    check(status+'_occupancy_visible',(await page.locator('.execution-list').innerText()).includes('hook'));
  }
  synthetic='running';await page.reload();await page.locator('.execution-snapshot>summary').click();await panel.locator('summary').click();
  await page.evaluate(()=>window.__hookStreams.forEach(s=>s.onerror?.(new Event('error'))));
  await page.getByText('连接断开 · 数据已过期',{exact:true}).waitFor({state:'visible'});
  check('offline_history_retained',(await panel.innerText()).includes(native.native_run_id));
  check('offline_running_becomes_unverified',(await panel.innerText()).includes('sessionStart · 终态待核对'));
  check('no_execution_routes',requests.every(r=>r.method==='GET'));
  check('no_browser_errors',errors.length===0);
  return {checks,requests,errors,scope:'built UI replay of actual native Application/Handler timeout; synthetic unfinished/connection states, no native execution from browser'};
}
