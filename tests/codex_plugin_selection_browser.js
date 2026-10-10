// Current built UI and real Application/Handler exports. The create receipt and
// HTTP/SSE delivery are controlled; this is not another native execution.
async page => {
  page.setDefaultTimeout(5000);await page.unrouteAll({behavior:'ignoreErrors'});
  const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
  const sid=fixture.sessions[0],first=fixture.responses['/api/sessions/'+sid].session.execution_snapshot.plugin_selections[0];
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  page.__scopedPlugin={checks,requests,errors};page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};},sid);
  await page.route('**/*',async route=>{
    const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=r.postData()?JSON.parse(r.postData()):null;requests.push({path,method:r.method(),body});
    if(path==='/api/commands'&&body?.kind==='create')return route.fulfill({status:202,json:{command_id:body.command_id,status:'accepted',session_id:sid}});
    if(fixture.responses[path])return route.fulfill({json:fixture.responses[path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'unmapped fixture'});
  });
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9465/');await page.locator('.execution-snapshot>summary').click();
  const section=page.getByRole('region',{name:'本次 CLI 插件选择'});let text=await section.innerText();
  check('native_cache_source_visible',text.includes(first.native_source)&&!text.includes('@inline'));
  check('source_and_cache_both_visible',text.includes(first.source_path)&&text.includes(first.cache_path));
  check('verified_plugin_visible',text.includes('原生包与组件清单匹配'));
  check('original_id_recovery_visible',text.includes('原 ID'));
  check('codex_change_entry_explained',text.includes('可在下方“原会话能力改配”'));
  await section.getByText('包内组件 · 1 项',{exact:true}).click();check('mcp_component_visible',(await section.innerText()).includes('mcp · plugin_service'));
  const mcp=page.getByRole('region',{name:'插件带入的 MCP'});
  check('bound_mcp_source_visible',(await mcp.innerText()).includes(first.native_source)&&(await mcp.innerText()).includes('绑定能力：plugin-1'));
  check('no_usage_claimed',(await mcp.innerText()).includes('历史成功调用：暂无证据'));
  check('unrelated_inheritance_separate',(await page.getByRole('region',{name:'继承的 MCP'}).innerText()).includes('baseline'));
  await section.scrollIntoViewIfNeeded();await page.screenshot({path:'.dev/w0208-codex-scoped-plugin/enabled-1440.png',fullPage:true});
  await page.locator('.session-row').filter({hasText:'version-two'}).click();await page.locator('.execution-snapshot>summary').click();
  const second=fixture.responses['/api/sessions/'+fixture.sessions[2]].session.execution_snapshot.plugin_selections[0];text=await section.innerText();
  check('version_two_keeps_own_source',text.includes(second.native_source)&&!text.includes(first.native_source));
  await page.locator('.session-row').filter({hasText:'disabled'}).click();await page.locator('.execution-snapshot>summary').click();text=await section.innerText();
  check('disabled_verified_visible',text.includes('禁用整包')&&text.includes('原生包与组件清单匹配'));
  await page.getByRole('button',{name:'＋ 新建',exact:true}).click();const select=page.locator('select[name="capability_selection:plugin-1"]');await select.waitFor();
  check('new_choice_defaults_inherit',await select.inputValue()==='inherit');await select.selectOption('disabled');
  check('supported_components_explained',(await page.locator('.capability-references').innerText()).includes('本地 MCP 插件'));
  await page.getByRole('button',{name:'创建会话',exact:true}).click();const created=requests.find(r=>r.path==='/api/commands'&&r.body?.kind==='create');
  check('choice_serialized',created?.body?.capability_selections?.some(s=>s.asset_id==='plugin-1'&&s.mode==='disabled'));
  check('client_cannot_supply_cache_binding',!JSON.stringify(created?.body).includes('cache_path')&&!JSON.stringify(created?.body).includes('native_source'));
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();
  if(await page.locator('.execution-snapshot').getAttribute('open')===null)await page.locator('.execution-snapshot>summary').click();await section.scrollIntoViewIfNeeded();
  check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));await page.screenshot({path:'.dev/w0208-codex-scoped-plugin/enabled-390.png',fullPage:true});
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));await section.getByText('已断开，原生状态待重新核对',{exact:true}).waitFor();
  check('disconnected_not_verified',await section.getByText('原生包与组件清单匹配',{exact:true}).count()===0);
  check('no_browser_errors',errors.length===0);
  return {checks,requests,errors,scope:'built UI using native Application/Handler exports; controlled create receipt and HTTP/SSE; no additional native/model/tool execution'};
}
