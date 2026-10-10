// Real native Application catalog responses, with controlled HTTP transport.
async page => {
  const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
  const sid=fixture.sessions.a,other=fixture.sessions.b;
  let fail=false,synthetic=false,directoryPhase="";
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};},sid);
  await page.route('**/*',async route=>{
    const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=r.postData()?JSON.parse(r.postData()):null;
    requests.push({path,method:r.method(),body});
    if(path.endsWith('/skills/catalog')) {
      if(fail)return route.fulfill({status:409,json:{error:'原生查询未开启；读取清单不会启动或恢复查询'}});
      const value=JSON.parse(JSON.stringify(directoryPhase ? fixture.responses["directory-"+directoryPhase] : fixture.responses[path]));
      if(synthetic)value.items.push(
        {name:'managed-fixture',description:'contract fixture only',source:'policySettings',aliases:[],command_advertised:true,description_listed:true,builtin:false,ambiguous:false,control:'unavailable',detail:'工作台尚不能独立排除此来源。'},
        {name:'conflict-fixture',description:'contract fixture only',source:'projectSettings',aliases:['shared-alias'],command_advertised:true,description_listed:true,builtin:false,ambiguous:true,control:'ambiguous',detail:'来源冲突，不能确认独立选配。'},
        {name:'unknown-fixture',description:'contract fixture only',source:'futureSource',aliases:[],command_advertised:false,description_listed:true,builtin:false,ambiguous:false,control:'unavailable',detail:'来源类别尚待核对。'});
      return route.fulfill({json:value});
    }
    if(fixture.responses[path])return route.fulfill({json:fixture.responses[path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':path.endsWith('.svg')?'image/svg+xml':'text/html'});
    return route.fulfill({status:404,body:'unmapped fixture'});
  });
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9455/');
  await page.locator('.execution-snapshot>summary').click();
  const panel=page.locator('details').filter({has:page.locator('summary').getByText('Claude Skill 来源清单',{exact:true})}).last();
  await panel.locator('summary').first().click();
  await panel.getByRole('button',{name:'读取当前原生清单',exact:true}).click();
  await panel.getByRole('status').waitFor();
  check('saved_native_identity_visible',(await panel.innerText()).includes(fixture.responses['/api/sessions/'+sid].session.scope.native_id));
  const user=panel.locator('.snapshot-asset').filter({hasText:'user-method · Skill 描述已报告'});
  check('unselected_user_source_visible',(await user.innerText()).includes('来源：用户')&&(await user.innerText()).includes('需先登记并核对入口'));
  check('unselected_path_not_invented',!(await user.innerText()).includes('快照绑定入口'));
  check('selected_plugin_and_alias_visible',(await panel.innerText()).includes('保存的资产：shared')&&(await panel.innerText()).includes('别名：'));
  await panel.getByText(/^内置 Skill 与仅命令条目/).click();
  check('builtin_skill_source_visible',(await panel.innerText()).includes('来源：CLI 内置 Skill'));
  check('builtin_not_labeled_as_registered',(await panel.locator('.snapshot-asset').filter({hasText:'claude-api · Skill 描述已报告'}).innerText()).includes('尚不能独立选配'));
  check('no_method_gate_claim',(await panel.innerText()).includes('不代表正文已加载、方法已使用或 Gate 已通过'));
  check('incomplete_source_coverage_visible',(await panel.innerText()).includes('不是完整磁盘扫描'));
  await panel.getByText(/^内置 Skill 与仅命令条目/).click();
  const directories=panel.getByRole('region',{name:'Claude 用户与项目 Skill 目录'});
  await directories.locator('summary').click();
  const expected=fixture.responses['/api/sessions/'+sid+'/skills/catalog'].directories;
  check('directory_paths_from_bound_profile',(await directories.innerText()).includes(expected.entries.find(e=>e.source==='userSettings').path));
  check('directory_project_path_and_key_visible',(await directories.innerText()).includes(expected.entries.find(e=>e.source==='projectSettings').path)&&(await directories.innerText()).includes('原生目录键：inherited'));
  check('directory_not_labeled_as_native_enablement',(await directories.innerText()).includes('不代表原生启用、已加载或已使用'));
  check('directory_scope_explicit',(await directories.innerText()).includes('受管、插件、旧 commands 目录及其他动态来源不在扫描范围'));
  check('directory_no_automatic_name_binding',(await directories.innerText()).includes('不按同名自动关联'));
  await directories.scrollIntoViewIfNeeded();
  await page.screenshot({path:'.dev/w0207-claude-directories/native-1440.png',fullPage:true});
  directoryPhase='partial';await panel.getByRole('button',{name:'读取当前原生清单',exact:true}).click();await panel.getByRole('status').waitFor();
  await directories.locator('summary').click();
  check('partial_directory_error_is_specific',(await directories.innerText()).includes('目录采样不完整')&&(await directories.innerText()).includes('元数据无效'));
  check('partial_directory_keeps_native_catalog',await panel.locator('.snapshot-asset').filter({hasText:'user-method · Skill 描述已报告'}).count()===1);
  check('directory_does_not_expose_private_body',!(await panel.innerText()).includes('PRIVATE_'));
  directoryPhase='ambiguous';await panel.getByRole('button',{name:'读取当前原生清单',exact:true}).click();await panel.getByRole('status').waitFor();
  await directories.locator('summary').click();
  check('file_aliases_are_ambiguous',await directories.getByText('来源有歧义',{exact:true}).count()===2);
  await directories.scrollIntoViewIfNeeded();await page.screenshot({path:'.dev/w0207-claude-directories/aliases-1440.png',fullPage:true});
  directoryPhase='';

  fail=true;await panel.getByRole('button',{name:'读取当前原生清单',exact:true}).click();
  await panel.getByRole('alert').waitFor();check('failed_read_clears_previous_catalog',await panel.locator('.snapshot-asset').count()===0);
  check('closed_query_reason_visible',(await panel.innerText()).includes('不会启动或恢复查询'));
  fail=false;synthetic=true;await panel.getByRole('button',{name:'读取当前原生清单',exact:true}).click();await panel.getByRole('status').waitFor();
  check('managed_fixture_limit_visible',(await panel.locator('.snapshot-asset').filter({hasText:'managed-fixture'}).innerText()).includes('管理员受管'));
  check('conflict_fixture_limit_visible',(await panel.locator('.snapshot-asset').filter({hasText:'conflict-fixture'}).innerText()).includes('来源或名称冲突'));
  check('unknown_fixture_is_not_reclassified',(await panel.innerText()).includes('未知类别（futureSource）'));
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await panel.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await directories.locator('summary').click();await directories.scrollIntoViewIfNeeded();
  await page.screenshot({path:'.dev/w0207-claude-directories/contracts-390.png',fullPage:true});
  await page.setViewportSize({width:1440,height:1100});await page.locator('.session-row').filter({hasText:'Skill B'}).click();
  await page.locator('.execution-snapshot>summary').click();await panel.locator('summary').first().click();
  check('session_switch_clears_old_catalog',await panel.locator('.snapshot-asset').count()===0);
  synthetic=false;await panel.getByRole('button',{name:'读取当前原生清单',exact:true}).click();await panel.getByRole('status').waitFor();
  check('second_session_own_identity',(await panel.innerText()).includes(fixture.responses['/api/sessions/'+other].session.scope.native_id));
  await directories.locator('summary').click();
  check('second_profile_has_own_directory',(await directories.innerText()).includes(fixture.responses['/api/sessions/'+other+'/skills/catalog'].directories.entries.find(e=>e.source==='userSettings').path)&&!(await directories.innerText()).includes(expected.entries.find(e=>e.source==='userSettings').path));
  check('excluded_plugin_not_reintroduced',!(await panel.innerText()).includes('保存的资产：shared'));
  check('requests_use_empty_body',requests.filter(r=>r.path.endsWith('/skills/catalog')).every(r=>r.method==='POST'&&JSON.stringify(r.body)==='{}'));
  check('no_execution_commands',requests.every(r=>r.path!=='/api/commands'));
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));
  await panel.getByText('工作台已断开，连接后可重新读取。',{exact:true}).waitFor({state:'attached'});
  await panel.locator('summary').first().click();
  check('disconnect_clears_catalog',await panel.locator('.snapshot-asset').count()===0);
  check('disconnect_disables_read',await panel.getByRole('button',{name:'读取当前原生清单',exact:true}).isDisabled());
  check('no_browser_errors',errors.length===0);
  return {checks,requests,errors,scope:'real native Application user/project/plugin/builtin data; synthetic managed/conflict/unknown display cases; controlled HTTP'};
}
