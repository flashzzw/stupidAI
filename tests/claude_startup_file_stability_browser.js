// Real Handler reports for local files; controlled SDK reports for system-policy races.
async page => {
  const fixture=__FIXTURE__,assets=__ASSETS__,output=__OUTPUT__,checks={},requests=[],errors=[];
  let phase='valid',failed=false;
  const path='/api/sessions/'+fixture.sessionId+'/plugins/startup-settings';
  const check=(name,value)=>{checks[name]=!!value;if(!value)throw Error(name);};
  page.setDefaultTimeout(6000);await page.unrouteAll({behavior:'ignoreErrors'});
  page.on('pageerror',error=>errors.push(error.message));
  await page.addInitScript(id=>{
    sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);
    window.EventSource=class{constructor(){window.__fileReadStream=this;setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};
  },fixture.sessionId);
  await page.route('**/*',async route=>{
    const r=route.request(),p=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];
    requests.push({path:p,method:r.method(),body:r.postData()});
    if(p===path)return failed?route.fulfill({status:409,json:{error:'读取期间会话绑定已变化，请重新读取'}}):route.fulfill({json:fixture.reports[phase]});
    if(fixture.responses[p])return route.fulfill({json:fixture.responses[p]});
    if(assets[p])return route.fulfill({path:assets[p],contentType:p.endsWith('.js')?'application/javascript':p.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:409,body:'No execution in source-read replay'});
  });
  await page.setViewportSize({width:1440,height:1050});await page.goto('http://127.0.0.1:9463/');
  await page.locator('.execution-snapshot>summary').click();
  await page.getByText('Claude 已安装插件登记',{exact:true}).click();
  const panel=page.locator('.startup-settings');
  await panel.locator(':scope>summary').click();
  check('no_automatic_read',!requests.some(r=>r.path===path));
  const sample=async mode=>{
    phase=mode;await panel.getByRole('button',{name:'读取设置来源',exact:true}).click();
    await panel.getByRole('status').waitFor();return panel.innerText();
  };
  let content=await sample('valid');
  check('real_handler_read_remains_available',content.includes('本次来源读取完成；完整启动配置仍待核对。'));
  check('file_hashes_preserved',fixture.reports.valid.files.filter(f=>f.sha256).every(f=>content.includes(f.sha256)));
  content=await sample('managed-replace');
  check('same_byte_replacement_is_incomplete',content.includes('本次来源读取不完整')&&content.includes('读取期间变化'));
  check('changed_paths_visible',fixture.reports['managed-replace'].files.filter(f=>f.status==='changed_during_read').every(f=>content.includes(f.path)));
  content=await sample('managed-before-open');
  check('resolve_open_race_is_incomplete',content.includes('本次来源读取不完整')&&content.includes('读取期间变化'));
  content=await sample('managed-stable-link');
  check('stable_link_is_readable',content.includes('本次来源读取完成'));
  const aliases=fixture.reports['managed-stable-link'].path_bindings.filter(p=>p.path!==p.resolved_path);
  await panel.getByText('本次路径解析',{exact:true}).click();
  content=await panel.innerText();
  check('alias_and_resolved_paths_visible',aliases.length>0&&aliases.every(p=>content.includes(p.path)&&content.includes('解析到 '+p.resolved_path)));
  check('no_private_settings_values',!content.includes('PRIVATE_PREFLIGHT_SENTINEL'));
  const resolution=panel.locator('details').filter({has:page.getByText('本次路径解析',{exact:true})});
  await resolution.scrollIntoViewIfNeeded();await resolution.screenshot({path:output+'/resolved-path-1440.png'});
  content=await sample('managed-retarget');
  check('retargeted_link_not_claimed_stable',content.includes('本次来源读取不完整')&&content.includes('读取期间变化'));
  check('full_policy_limits_remain',content.includes('未获取最新服务端策略')&&content.includes('不能据此确认完整权限或允许已安装插件启停'));
  content=await sample('malformed');
  check('existing_invalid_json_diagnostic',content.includes('JSON 无效')&&content.includes('本次来源读取不完整'));
  failed=true;await panel.getByRole('button',{name:'读取设置来源',exact:true}).click();await panel.getByRole('alert').waitFor();
  check('failed_refresh_clears_observation',await panel.getByRole('status').count()===0&&!((await panel.innerText()).includes('本次路径解析')));
  failed=false;await sample('managed-retarget');
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();
  const changed=panel.locator('.snapshot-asset').filter({has:page.getByText('读取期间变化',{exact:true})}).first();
  await changed.scrollIntoViewIfNeeded();
  check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await changed.screenshot({path:output+'/changed-source-390.png'});
  await page.evaluate(()=>window.__fileReadStream.onerror?.(new Event('error')));
  await page.getByText('连接断开 · 数据已过期',{exact:true}).waitFor();
  await page.getByText('Claude 已安装插件登记',{exact:true}).click();await page.locator('.startup-settings>summary').click();
  check('disconnected_read_disabled',await page.locator('.startup-settings').getByRole('button',{name:'读取设置来源',exact:true}).isDisabled());
  check('only_empty_bound_read_requests',requests.filter(r=>r.path===path).every(r=>r.method==='POST'&&r.body==='{}'));
  check('no_execution_command',!requests.some(r=>r.path==='/api/commands'));
  check('no_page_errors',errors.length===0);
  return {checks,requests,errors,passed:Object.values(checks).every(Boolean),scope:fixture.provenance};
}
