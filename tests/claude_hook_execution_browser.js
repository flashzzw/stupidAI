// Native Application data first, then explicitly synthetic unfinished states.
async page => {
  page.setDefaultTimeout(8000);await page.unrouteAll({behavior:'ignoreErrors'});
  const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
  let synthetic='';
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  page.__hookExecution={checks,requests,errors};page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};},fixture.sessions.a);
  await page.route('**/*',async route=>{
    const request=route.request(),path=request.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];requests.push({path,method:request.method()});
    if(fixture.responses[path]){
      const response=JSON.parse(JSON.stringify(fixture.responses[path]));
      if(synthetic&&response.session){const hook=response.session.view.hook_executions[0];hook.status=synthetic;delete hook.finished_at;delete hook.exit_code;response.session.view.executions=[{id:hook.id,kind:'hook',status:synthetic}];}
      return route.fulfill({json:response});
    }
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'unmapped fixture'});
  });
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9459/');
  await page.locator('.execution-snapshot>summary').click();
  const panel=page.locator('.hook-executions');await panel.locator('summary').click();
  check('native_success_visible',(await panel.innerText()).includes('SessionStart · 成功结束'));
  check('native_exit_zero_visible',(await panel.innerText()).includes('原生退出码：0'));
  const id=fixture.responses['/api/sessions/'+fixture.sessions.a].session.view.hook_executions[0].id;
  check('native_invocation_id_visible',(await panel.innerText()).includes(id));
  check('source_not_guessed',(await panel.innerText()).includes('来源未报告，不能据事件名称指定某个插件'));
  check('startup_not_given_model_turn',(await panel.innerText()).includes('未关联模型轮次'));
  await page.locator('.session-row').filter({hasText:'Plugin B'}).click();await page.locator('.execution-snapshot>summary').click();await panel.locator('summary').click();
  check('native_failure_visible',(await panel.innerText()).includes('SessionStart · 失败结束'));
  check('native_exit_two_visible',(await panel.innerText()).includes('原生退出码：2'));
  check('private_diagnostics_absent',!(await panel.innerText()).includes('PRIVATE_'));
  check('success_identity_not_carried_to_failure',!(await panel.innerText()).includes(id));
  await page.screenshot({path:'.dev/w0208-hook-execution/failure-1440.png',fullPage:true});
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await panel.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.screenshot({path:'.dev/w0208-hook-execution/failure-390.png',fullPage:true});
  await page.setViewportSize({width:1440,height:1100});
  for(const [status,label] of [['running','执行中'],['unknown','终态待核对']]){
    synthetic=status;await page.reload();await page.locator('.execution-snapshot>summary').click();await panel.locator('summary').click();
    check(status+'_fixture_visible',(await panel.innerText()).includes('SessionStart · '+label));
    check(status+'_not_given_exit_code',!(await panel.innerText()).includes('原生退出码'));
    check(status+'_occupancy_visible',(await page.locator('.execution-list').innerText()).includes('hook'));
  }
  check('no_native_mutations',requests.every(r=>r.method==='GET'));
  check('no_browser_errors',errors.length===0);
  return {checks,requests,errors,scope:'native Application success/error records; controlled browser transport; running/unknown states are synthetic'};
}
