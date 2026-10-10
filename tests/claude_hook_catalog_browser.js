// Native Application responses; HTTP and connection events are controlled.
async page => {
  page.setDefaultTimeout(8000); await page.unroute('**/*');
  const fixture=__FIXTURE__, assets=__ASSETS__, checks={}, requests=[], errors=[];
  const sid=fixture.sessions.a; let fail=false, synthetic=false;
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};},sid);
  await page.route('**/*',async route=>{
    const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=r.postData()?JSON.parse(r.postData()):null;
    requests.push({path,method:r.method(),body});
    if(path.endsWith('/hooks/catalog')) {
      if(fail)return route.fulfill({status:409,json:{error:'原生查询未开启；读取清单不会启动或恢复查询'}});
      const value=JSON.parse(JSON.stringify(fixture.responses[path]));
      if(synthetic){value.policy.managed_only=true;value.policy.policy_unreadable=true;value.policy.settings_error_count=2;
        value.items.push({...value.items[0],event:'FutureEvent',source:'futureSource',plugin_name:'',conditional:true,background:true,runs_once:true,timeout_seconds:4});}
      return route.fulfill({json:value});
    }
    if(fixture.responses[path])return route.fulfill({json:fixture.responses[path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':path.endsWith('.svg')?'image/svg+xml':'text/html'});
    return route.fulfill({status:404,body:'unmapped fixture'});
  });
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9458/',{waitUntil:'domcontentloaded',timeout:8000});
  await page.locator('.execution-snapshot>summary').click();
  const panel=page.locator('.hook-catalog');await panel.locator('summary').first().click();
  check('no_automatic_read',requests.every(r=>!r.path.endsWith('/hooks/catalog')));
  await panel.getByRole('button',{name:'读取当前 hooks',exact:true}).click();await panel.getByRole('status').waitFor();
  check('native_identity_visible',(await panel.innerText()).includes(fixture.responses['/api/sessions/'+sid].session.scope.native_id));
  check('three_native_hooks_visible',await panel.locator('.snapshot-asset').count()===3);
  check('user_project_plugin_sources_visible',(await panel.innerText()).includes('来源：用户') && (await panel.innerText()).includes('来源：项目') && (await panel.innerText()).includes('catalog-hook@hook-local'));
  check('registered_is_not_execution_success',await panel.getByText('已登记 · 执行结果未核对',{exact:true}).count()===3);
  check('secrets_not_rendered',!(await panel.innerText()).includes('PRIVATE_'));
  check('incomplete_scope_visible',(await panel.innerText()).includes('覆盖不完整'));
  await panel.getByText('定义摘要',{exact:true}).first().click();
  check('definition_digest_visible',(await panel.innerText()).includes(fixture.responses['/api/sessions/'+sid+'/hooks/catalog'].items[0].definition_hash));
  await page.screenshot({path:'.dev/w0208-hooks/native-1440.png',fullPage:true});
  fail=true;await panel.getByRole('button',{name:'读取当前 hooks',exact:true}).click();await panel.getByRole('alert').waitFor();
  check('failure_clears_previous_rows',await panel.locator('.snapshot-asset').count()===0);
  check('failure_does_not_offer_automatic_restart',(await panel.innerText()).includes('不会启动或恢复查询'));
  fail=false;synthetic=true;await panel.getByRole('button',{name:'读取当前 hooks',exact:true}).click();await panel.getByRole('status').waitFor();
  check('managed_unknown_policy_is_visible',(await panel.innerText()).includes('受管策略无法读取') && (await panel.innerText()).includes('仅允许受管 hooks'));
  check('unknown_source_not_reclassified',(await panel.innerText()).includes('未知类别（futureSource）'));
  check('skipped_settings_visible',(await panel.innerText()).includes('2 项配置解析错误'));
  check('conditional_and_background_visible',(await panel.innerText()).includes('含额外执行条件') && (await panel.innerText()).includes('后台执行') && (await panel.innerText()).includes('仅执行一次'));
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await panel.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.screenshot({path:'.dev/w0208-hooks/contracts-390.png',fullPage:true});
  await page.setViewportSize({width:1440,height:1100});await page.locator('.session-row').filter({hasText:'Skill B'}).click();
  await page.locator('.execution-snapshot>summary').click();await panel.locator('summary').first().click();
  check('switch_clears_old_rows',await panel.locator('.snapshot-asset').count()===0);
  synthetic=false;await panel.getByRole('button',{name:'读取当前 hooks',exact:true}).click();await panel.getByRole('status').waitFor();
  check('native_disabled_rows_visible',await panel.getByText('原生报告已禁用',{exact:true}).count()===3);
  check('native_disabled_policy_visible',(await panel.innerText()).includes('原生设置：关闭 hooks'));
  check('only_empty_read_requests',requests.filter(r=>r.path.endsWith('/hooks/catalog')).every(r=>r.method==='POST'&&JSON.stringify(r.body)==='{}'));
  check('no_execution_commands',requests.every(r=>r.path!=='/api/commands'));
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));await panel.getByText('工作台已断开，连接后可重新读取。',{exact:true}).waitFor({state:'attached'});
  await panel.locator('summary').first().click();
  check('disconnect_clears_rows',await panel.locator('.snapshot-asset').count()===0);
  check('disconnect_disables_read',await panel.getByRole('button',{name:'读取当前 hooks',exact:true}).isDisabled());
  check('no_browser_errors',errors.length===0);
  return {checks,requests,errors,scope:'native Application user/project/plugin/disabled fixtures; synthetic managed/unknown/error display cases; controlled HTTP'};
}
