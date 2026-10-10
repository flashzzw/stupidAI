// Runtime contract-test output in an Application response shell; no native call.
async page => {
  page.setDefaultTimeout(8000);await page.unrouteAll({behavior:'ignoreErrors'});
  const fixture=__FIXTURE__,views=__VIEWS__,assets=__ASSETS__,checks={},errors=[],requests=[];
  let mode='success';const sid=fixture.sessions.a;
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};},sid);
  await page.route('**/*',async route=>{
    const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];requests.push({path,method:r.method()});
    if(fixture.responses[path]){
      const response=JSON.parse(JSON.stringify(fixture.responses[path]));
      if(response.session)response.session.view.capability_evidence=views[mode].capability_evidence;
      return route.fulfill({json:response});
    }
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'unmapped fixture'});
  });
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9460/');
  await page.locator('.execution-snapshot>summary').click();
  const panel=page.getByRole('region',{name:'插件带入的 MCP'});
  await panel.waitFor();let body=await panel.innerText();
  check('selected_plugin_mcp_group',body.includes('plugin:fixture:identity')&&body.includes('绑定能力：plugin'));
  check('native_plugin_source_visible',body.includes('原生来源：CLI 插件'));
  check('successful_usage_visible',body.includes('历史成功调用：whoami'));
  check('selected_not_called_inherited',!body.includes('继承配置'));
  await page.screenshot({path:'.dev/w0208-plugin-mcp/usage-1440.png',fullPage:true});
  for(const state of ['error','pending','offline','normalized-collision']){
    mode=state;await page.reload();await page.locator('.execution-snapshot>summary').click();body=await panel.innerText();
    if(state==='offline'){
      check('offline_keeps_historical_usage',body.includes('历史成功调用：whoami'));
      check('offline_current_binding_unverified',body.includes('未核对一致')&&body.includes('尚未采样'));
    }else check(state+'_not_counted_as_success',body.includes('历史成功调用：暂无证据'));
    if(state==='normalized-collision'){
      check('collision_source_not_guessed',body.includes('原生来源：未知来源'));
      check('inherited_collision_separate',(await page.getByRole('region',{name:'继承的 MCP'}).innerText()).includes('plugin_fixture_identity'));
    }
  }
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await panel.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.screenshot({path:'.dev/w0208-plugin-mcp/usage-390.png',fullPage:true});
  check('no_writes',requests.every(r=>r.method==='GET'));check('no_browser_errors',errors.length===0);
  return {checks,requests,errors,scope:'controlled application responses with actual Runtime fixture projection; no native/plugin/MCP invocation'};
}
