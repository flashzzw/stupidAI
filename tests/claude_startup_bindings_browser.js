// Real Handler binding-conflict responses; failed refresh and disconnect are UI fixtures.
async page => {
  const fixture=__FIXTURE__,assets=__ASSETS__,output=__OUTPUT__,checks={},requests=[],errors=[];
  const sid=fixture.results.session_id,base='/api/sessions/'+sid,sourceKey=fixture.results.catalog.entries[0].source_key;
  let phase='bindings-route',failed=false;
  const check=(name,value)=>{checks[name]=!!value;if(!value)throw Error(name);};
  page.setDefaultTimeout(6000);await page.unrouteAll({behavior:'ignoreErrors'});page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{
    sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);
    window.EventSource=class{constructor(){window.bindingEvents=this;setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};
  },sid);
  await page.route('**/*',async route=>{
    const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];
    requests.push({path,method:r.method(),body:r.postData()});
    if(path.startsWith('/favicon.'))return route.fulfill({status:204});
    if(path===base+'/plugins/startup-settings')return failed?route.fulfill({status:409,json:{error:'读取期间会话绑定已变化，请重新读取'}}):route.fulfill({json:fixture.results['policy_read_'+phase]});
    if(path===base+'/plugins/installed')return route.fulfill({json:fixture.results.catalog});
    if(path===base+'/plugins/settings-preview')return route.fulfill({json:fixture.results['policy_preview_'+phase]});
    if(fixture.responses[path])return route.fulfill({json:fixture.responses[path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:409,body:'No execution in binding-source replay'});
  });
  await page.setViewportSize({width:1440,height:1050});await page.goto('http://127.0.0.1:9463/');
  await page.locator('.execution-snapshot>summary').click();await page.getByText('Claude 已安装插件登记',{exact:true}).click();
  const panel=page.locator('details.startup-settings');await panel.locator(':scope>summary').click();
  check('no_automatic_source_read',!requests.some(r=>r.path.endsWith('/plugins/startup-settings')));
  const sample=async mode=>{phase=mode;await panel.getByRole('button',{name:'读取设置来源',exact:true}).click();await panel.getByRole('status').waitFor();return panel.innerText();};
  let content=await sample('bindings-route');
  check('read_is_distinct_from_binding_admission',content.includes('本次来源读取完成')&&content.includes('来源中发现启动绑定冲突'));
  check('route_conflict_and_observed_file_visible',content.includes('路由或凭据环境覆盖 · 已读取文件')&&content.includes('/etc/claude-code/managed-settings.json'));
  check('observed_declarations_not_complete_effective_state',content.includes('这里只报告已观察到的声明')&&content.includes('不表示所有来源都已核对'));
  await panel.locator('.startup-binding-conflicts').scrollIntoViewIfNeeded();await page.screenshot({path:output+'/binding-conflict-1440.png',fullPage:true});
  await page.getByRole('button',{name:'读取安装登记',exact:true}).click();await page.getByText('支持范围内的登记采样完成 · 1 条安装记录',{exact:true}).waitFor();
  const preview=page.locator('.snapshot-asset>.startup-settings');
  await preview.getByRole('button',{name:sourceKey+'：核对排除设置',exact:true}).click();await preview.getByText('核对时间：',{exact:false}).waitFor();
  content=await preview.innerText();
  check('actual_preview_refusal_visible',content.includes('启动绑定冲突')&&content.includes('预览未完成'));
  check('refusal_before_private_file',content.includes('未生成临时设置文件')&&!content.includes('原生观察值：'));
  content=await sample('bindings-state');check('state_conflict_visible',content.includes('状态目录环境覆盖'));
  content=await sample('bindings-helper');check('credential_helper_conflict_visible',content.includes('凭据辅助程序'));
  content=await sample('bindings-invalid-env');check('discarded_environment_format_not_hidden',content.includes('环境变量格式无效 · 已读取文件'));
  check('no_values_or_commands_in_diagnostics',!content.includes('PRIVATE_')&&!content.includes('python3 ')&&!content.includes('OWNED_INVALID'));
  content=await sample('platform-command');
  check('binding_conflict_clears_on_new_source',await panel.locator('.startup-binding-conflicts').count()===0);
  check('existing_policy_helper_gap_preserved',content.includes('按平台选择的策略辅助配置'));
  failed=true;await panel.getByRole('button',{name:'读取设置来源',exact:true}).click();await panel.getByRole('alert').waitFor();
  check('failed_read_clears_old_observation',await panel.getByRole('status').count()===0&&await panel.locator('.startup-binding-conflicts').count()===0);
  failed=false;await sample('bindings-route');
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await panel.locator('.startup-binding-conflicts').scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await page.screenshot({path:output+'/binding-conflict-390.png',fullPage:true});
  await page.evaluate(()=>window.bindingEvents.onerror?.(new Event('error')));await page.getByText('连接断开 · 数据已过期',{exact:true}).waitFor();
  await page.getByText('Claude 已安装插件登记',{exact:true}).click();await page.locator('details.startup-settings>summary').click();
  check('disconnected_read_disabled',await page.locator('details.startup-settings').getByRole('button',{name:'读取设置来源',exact:true}).isDisabled());
  check('bound_read_requests_only',requests.filter(r=>r.path===base+'/plugins/startup-settings').every(r=>r.method==='POST'&&r.body==='{}'));
  check('no_execution_commands',!requests.some(r=>r.path==='/api/commands'));
  check('no_javascript_errors',errors.length===0);
  return {checks,requests,errors,passed:Object.values(checks).every(Boolean),scope:'current real Application/Handler file binding declarations and refused preview; controlled refresh failure and disconnect'};
}
