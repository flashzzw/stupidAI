// Real Application responses, with controlled policy/disconnect/legacy display transitions.
async page => {
  const fixture=__FIXTURE__, assets=__ASSETS__, checks={}, requests=[], errors=[];
  const sid=fixture.session_id, detailPath='/api/sessions/'+sid;
  const current=fixture.responses[detailPath].session;
  const original=JSON.parse(JSON.stringify(current)), check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{
    sessionStorage.setItem('netcap.selected.v1',id);
    window.EventSource=class{constructor(){this.listeners={};window.guardEvents=this;setTimeout(()=>this.onopen?.(),0);}addEventListener(k,f){this.listeners[k]=f;}close(){}};
  },sid);
  await page.route('**/*',async route=>{
    const path=route.request().url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];requests.push(path);
    if(fixture.responses[path])return route.fulfill({json:fixture.responses[path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'fixture'});
  });
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9455/');
  await page.locator('.execution-snapshot > summary').click();
  const panel=page.getByRole('region',{name:'本地设置自动应用限制'});
  await panel.getByText('本地设置自动应用限制 · 已注册',{exact:true}).waitFor();
  const path=original.view.settings_guard.changes[0].path;
  check('native_guard_registration', (await panel.innerText()).includes('已为项目/本地设置注册'));
  check('exact_changed_path',(await panel.innerText()).includes(path));
  check('decision_not_native_confirmation',(await panel.innerText()).includes('已请求阻止')&&(await panel.innerText()).includes('不是全部配置冻结证明'));
  check('source_gate_preserved',(await panel.innerText()).includes('来源文件变化仍会阻止后续输入'));
  check('managed_limits_visible',(await panel.innerText()).includes('受管策略、脚本资源和其他来源未被固定'));
  await panel.screenshot({path:'.dev/w0208-settings-guard/guard-1440.png'});
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await panel.scrollIntoViewIfNeeded();
  check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await panel.screenshot({path:'.dev/w0208-settings-guard/guard-390.png'});
  await page.setViewportSize({width:1440,height:1100});
  const refresh=async()=>{
    for(const p of ['/api/workspace','/api/snapshot',detailPath])fixture.responses[p].sequence+=100;
    await page.evaluate(()=>window.guardEvents.listeners.change());
  };
  current.view.settings_guard.state='unavailable';current.view.settings_guard.detail='当前原生 hooks 已禁用，不能确认阻止机制可用。';await refresh();
  await panel.getByText('本地设置自动应用限制 · 当前不可用',{exact:true}).waitFor();
  check('disabled_policy_is_unavailable',(await panel.innerText()).includes('hooks 已禁用'));
  current.view.settings_guard=JSON.parse(JSON.stringify(original.view.settings_guard));current.view.settings_guard.version=2;await refresh();
  await panel.getByText('本地设置自动应用限制 · 当前未核对',{exact:true}).waitFor();
  check('wrong_version_not_registered',!(await panel.innerText()).includes(' · 已注册'));
  check('wrong_version_hides_changes',await panel.locator('.snapshot-asset').count()===0);
  current.view.settings_guard=JSON.parse(JSON.stringify(original.view.settings_guard));current.view.native_status='sdk_offline';await refresh();
  // The visible state is already unknown; wait for the matching history to return.
  await panel.getByText(path,{exact:true}).waitFor();
  check('native_offline_not_registered',(await panel.innerText()).includes('当前未核对'));
  check('offline_keeps_labeled_records',(await panel.innerText()).includes('最近的阻止决策记录'));
  current.view.native_status='sdk_live';await refresh();await panel.getByText('本地设置自动应用限制 · 已注册',{exact:true}).waitFor();
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));
  await panel.getByText('本地设置自动应用限制 · 当前未核对',{exact:true}).waitFor();
  check('transport_offline_not_registered',(await panel.innerText()).includes('旧记录不能证明当前仍能阻止自动应用'));
  await page.evaluate(()=>window.dispatchEvent(new Event('online')));
  await panel.getByText('本地设置自动应用限制 · 已注册',{exact:true}).waitFor();
  delete current.execution_snapshot.claude_local_settings.guard_version;current.execution_snapshot.id+='-legacy';await refresh();
  await page.getByText('此执行未登记本地设置自动应用限制；来源版本记录仍独立核对。',{exact:true}).waitFor();
  check('legacy_does_not_invent_guard',await panel.count()===0);
  check('no_commands',requests.every(p=>p!=='/api/commands'));
  check('no_page_errors',errors.length===0);
  return {checks,requests,errors,scope:'real Application guard/source evidence; policy and connection changes are HTTP/SSE replay only'};
}
