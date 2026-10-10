// Built UI with actual mixed-plugin Application/Handler records. HTTP/SSE and
// the create receipt are controlled; no additional native execution occurs.
async page => {
  page.setDefaultTimeout(5000);await page.unrouteAll({behavior:'ignoreErrors'});
  const fixture=__FIXTURE__,assets=__ASSETS__,sid=fixture.session,checks={},requests=[],errors=[];
  const selection=fixture.responses['/api/sessions/'+sid].session.execution_snapshot.plugin_selections[0];
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};},sid);
  await page.route('**/*',async route=>{
    const request=route.request(),path=request.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=request.postData()?JSON.parse(request.postData()):null;
    requests.push({path,method:request.method(),body});
    if(path==='/api/commands'&&body?.kind==='create')return route.fulfill({status:202,json:{command_id:body.command_id,status:'accepted',session_id:sid}});
    if(fixture.responses[path])return route.fulfill({json:fixture.responses[path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'unmapped fixture path'});
  });
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9467/');await page.locator('.execution-snapshot>summary').click();
  const section=page.getByRole('region',{name:'本次 CLI 插件选择'});
  let text=await section.innerText();
  check('policy_coverage_visible',text.includes('固定插件包、MCP 与整包策略已核对'));
  check('no_skill_inventory_claim',!text.includes('原生包与组件清单匹配'));
  check('source_and_private_version_visible',text.includes(selection.native_source)&&text.includes(selection.source_path)&&text.includes(selection.cache_path));
  check('context_evidence_separate',text.includes('Skill 描述及正文载入请查看逐轮上下文'));
  check('history_retention_explained',text.includes('历史内容不会因禁用而删除'));
  check('unsupported_hooks_visible',text.includes('含 hooks 的插件尚未接通')&&!text.includes('仅含 Skill 或含 hooks'));
  await section.getByText('包内组件 · 2 项',{exact:true}).click();text=await section.innerText();
  check('mcp_component_visible',text.includes('mcp · mixed_service_v2'));
  check('skill_component_visible',text.includes('skill · mixed:method')&&text.includes('skills/method/SKILL.md'));
  const mcp=page.getByRole('region',{name:'插件带入的 MCP'});
  check('mcp_provenance_retained',(await mcp.innerText()).includes(selection.native_source));
  check('no_successful_tool_claim',(await mcp.innerText()).includes('历史成功调用：暂无证据'));
  await section.scrollIntoViewIfNeeded();await page.screenshot({path:'.dev/w0208-mixed-plugins/desktop.png',fullPage:true});
  await page.getByRole('button',{name:'＋ 新建',exact:true}).click();const choose=page.locator('select[name="capability_selection:mixed"]');await choose.waitFor();
  check('creation_defaults_to_inherit',await choose.inputValue()==='inherit');await choose.selectOption('disabled');
  check('mixed_support_explained',(await page.locator('.capability-references').innerText()).includes('标准 Skill 插件及两者混合包'));
  await page.getByRole('button',{name:'创建会话',exact:true}).click();const command=requests.find(r=>r.path==='/api/commands'&&r.body?.kind==='create');
  check('whole_selection_serialized',command?.body?.capability_selections?.some(s=>s.asset_id==='mixed'&&s.mode==='disabled'));
  check('browser_does_not_supply_native_policy',!JSON.stringify(command?.body).includes('disabledPluginIds')&&!JSON.stringify(command?.body).includes('cache_path'));
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();
  if(await page.locator('.execution-snapshot').getAttribute('open')===null)await page.locator('.execution-snapshot>summary').click();await section.scrollIntoViewIfNeeded();
  check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));await page.screenshot({path:'.dev/w0208-mixed-plugins/mobile.png',fullPage:true});
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));await section.getByText('已断开，原生状态待重新核对',{exact:true}).waitFor();
  check('offline_clears_current_verification',await section.getByText('固定插件包、MCP 与整包策略已核对',{exact:true}).count()===0);
  check('no_browser_errors',errors.length===0);
  return {checks,requests,errors,scope:'built UI; real Application/Handler data; controlled HTTP/SSE and create acknowledgement; no additional model or native execution'};
}
