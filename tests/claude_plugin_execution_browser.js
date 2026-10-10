// Replay final WSL Application snapshot; HTTP/SSE transport is controlled.
// Historical observations remain as recorded, including the final source drift.
async page => {
  page.setDefaultTimeout(8000);
  await page.unrouteAll({behavior:'ignoreErrors'});
  const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  page.__pluginExecution={checks,requests,errors};
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{
    sessionStorage.setItem('netcap.selected.v1',id);
    window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};
  },fixture.sessions.B);
  await page.route('**/*',async route=>{
    const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];
    requests.push({path,method:r.method()});
    if(fixture.responses[path])return route.fulfill({json:fixture.responses[path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'unmapped replay route'});
  });
  await page.setViewportSize({width:1440,height:1100});
  await page.goto('http://127.0.0.1:9461/');
  await page.locator('.execution-snapshot>summary').click();
  const mcp=page.getByRole('region',{name:'插件带入的 MCP'}),hooks=page.locator('.hook-executions');
  let body=await mcp.innerText();
  check('native_plugin_binding_visible',body.includes('plugin:plugin-suite:identity')&&body.includes('绑定能力：suite'));
  check('native_plugin_source_visible',body.includes('原生来源：CLI 插件'));
  check('native_success_history_visible',body.includes('历史成功调用：whoami'));
  check('inherited_service_separate',(await page.getByRole('region',{name:'继承的 MCP'}).innerText()).includes('inherited'));
  await hooks.locator('summary').click();body=await hooks.innerText();
  check('pre_and_post_success_visible',body.includes('PreToolUse · 成功结束')&&body.includes('PostToolUse · 成功结束'));
  const b=fixture.responses['/api/sessions/'+fixture.sessions.B].session;
  const nativeHooks=b.view.hook_executions.filter(h=>h.operation);
  check('actual_operations_and_invocation_ids',nativeHooks.length===4&&nativeHooks.every(h=>body.includes(h.id)&&body.includes(h.operation)));
  check('hook_source_not_guessed',body.includes('来源未报告，不能据事件名称指定某个插件'));
  await mcp.scrollIntoViewIfNeeded();
  await page.screenshot({path:'.dev/w0208-plugin-execution/native-1440.png',fullPage:true});
  await page.locator('.session-row').filter({hasText:'hook-failure'}).click();
  await page.locator('.execution-snapshot>summary').click();await hooks.locator('summary').click();
  body=await hooks.innerText();
  check('native_pre_tool_failure_visible',body.includes('PreToolUse · 失败结束'));
  check('native_exit_two_visible',body.includes('原生退出码：2'));
  check('failed_tool_not_successful_usage',(await mcp.innerText()).includes('历史成功调用：暂无证据'));
  check('hook_private_command_absent',!body.includes('python3')&&!body.includes('hook.py'));
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await hooks.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.screenshot({path:'.dev/w0208-plugin-execution/native-failure-390.png',fullPage:true});
  await page.setViewportSize({width:1440,height:1100});
  await page.locator('.session-row').filter({hasText:'disabled'}).click();await page.locator('.execution-snapshot>summary').click();
  check('disabled_has_no_usage',(await mcp.innerText()).includes('历史成功调用：暂无证据'));
  check('no_writes',requests.every(r=>r.method==='GET'));check('no_browser_errors',errors.length===0);
  return {checks,requests,errors,scope:'built UI replay of final WSL Application snapshot and real tool/hook history; controlled HTTP/SSE, no additional native/model execution, no claim of current source verification'};
}
