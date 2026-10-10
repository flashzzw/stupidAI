// Real native Handler responses; mismatch/cleanup and lifecycle are controlled UI cases.
async page => {
  const fixture=__FIXTURE__,assets=__ASSETS__,output=__OUTPUT__,checks={},requests=[],errors=[];
  const sid=fixture.results.session_id,base='/api/sessions/'+sid,sourceKey=fixture.results.catalog.entries[0].source_key;
  let phase='disabled',fail=false,hold=false,release;
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  const report=()=>{
    const value=JSON.parse(JSON.stringify(fixture.results[phase==='mismatch'||phase==='retained'||phase.startsWith('preservation-')?'disabled':phase]));
    if(phase==='mismatch'){value.validation.plugin_settings[0].actual_enabled=true;value.validation.plugin_settings[0].matches=false;}
    if(phase==='retained'){value.cleanup.state='retained';value.cleanup.issues=['派生设置文件仍在使用，尚未回收'];}
    if(phase==='preservation-missing')delete value.validation.preservation;
    if(phase==='preservation-permissions'){value.validation.preservation.matches=false;value.validation.preservation.differences=['permission_rules'];value.problems=['原文件与派生文件的其他有效设置、来源或权限规则未通过保留核对'];}
    if(phase==='preservation-other-plugin'){value.validation.preservation.matches=false;value.validation.preservation.differences=['effective_settings'];value.problems=['原文件与派生文件的其他有效设置、来源或权限规则未通过保留核对'];}
    return value;
  };
  const catalog=()=>({...fixture.results.catalog,source_version:report().source_version,source_files:report().source_files});
  page.setDefaultTimeout(6000);await page.unrouteAll({behavior:'ignoreErrors'});page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{
    sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);
    window.EventSource=class{constructor(){this.listeners={};window.previewEvents=this;setTimeout(()=>this.onopen?.(),0);}addEventListener(n,fn){this.listeners[n]=fn;}close(){}};
  },sid);
  await page.route('**/*',async route=>{
    const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];
    requests.push({path,method:r.method(),body:r.postData()?JSON.parse(r.postData()):null});
    if(path.startsWith('/favicon.'))return route.fulfill({status:204});
    if(path===base+'/plugins/installed')return route.fulfill({json:catalog()});
    if(path===base+'/plugins/settings-preview'){
      const value=report();
      if(hold)await new Promise(resolve=>{release=resolve;});
      return fail?route.fulfill({status:409,json:fixture.results.stale_error}):route.fulfill({json:value});
    }
    if(fixture.responses[path])return route.fulfill({json:fixture.responses[path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:409,body:'Unmapped metadata preview request'});
  });
  await page.setViewportSize({width:1440,height:1050});await page.goto('http://127.0.0.1:9463/');
  await page.locator('.execution-snapshot>summary').click();
  const panel=page.locator('details.skill-discovery').filter({has:page.getByText('Claude 已安装插件登记',{exact:true})});
  await panel.locator(':scope>summary').click();
  const read=async()=>{await panel.getByRole('button',{name:'读取安装登记',exact:true}).click();await panel.getByText('支持范围内的登记采样完成 · 1 条安装记录',{exact:true}).waitFor();};
  check('no_automatic_preview',!requests.some(r=>r.path.endsWith('/plugins/settings-preview')));
  await read();
  await panel.getByText('本次登记与声明的文件版本',{exact:true}).click();
  check('raw_source_version_visible',(await panel.innerText()).includes(catalog().source_version));
  const content=await panel.innerText();check('all_metadata_files_and_hashes_visible',catalog().source_files.every(f=>content.includes(f.path)&&content.includes(f.sha256)));
  const preview=panel.locator('.snapshot-asset>.startup-settings');
  const run=async mode=>{await preview.getByRole('button',{name:sourceKey+'：'+(mode==='enabled'?'核对启用设置':'核对排除设置'),exact:true}).click();await preview.getByRole('status').waitFor();await preview.getByText('核对时间：',{exact:false}).waitFor();};
  await run('disabled');
  check('disabled_native_value_visible',(await preview.innerText()).includes('原生观察值：排除'));
  check('preview_not_applied',(await preview.innerText()).includes('目标设置核对相符；本会话配置未改变。'));
  check('other_settings_and_permissions_preserved_visible',(await preview.innerText()).includes('其他有效设置、来源、已采用参数和权限规则：本次观察一致。'));
  check('observation_is_not_execution_permission',(await preview.innerText()).includes('不证明当前会话的完整权限或后续策略保持不变'));
  check('cleanup_released_visible',(await preview.innerText()).includes('临时设置文件已回收'));
  check('full_policy_and_execution_gap_visible',(await preview.innerText()).includes('正式启停尚未开放'));
  await preview.getByText('本次设置文件',{exact:true}).click();
  check('original_and_derived_paths_visible',(await preview.innerText()).includes(fixture.results.disabled.artifact.original.path)&&(await preview.innerText()).includes(fixture.results.disabled.artifact.file.path));
  check('private_mode_visible',(await preview.innerText()).includes('权限 0600'));
  await preview.locator('.settings-preservation').scrollIntoViewIfNeeded();
  await page.screenshot({path:output+'/preservation-1440.png'});
  phase='enabled';await run('enabled');
  check('enabled_native_value_visible',(await preview.innerText()).includes('原生观察值：启用'));
  phase='invalid';await read();await run('disabled');
  check('invalid_schema_visible',(await preview.innerText()).includes('原生设置格式无效。')&&(await preview.innerText()).includes('permissions.allow'));
  phase='mismatch';await read();await run('disabled');
  check('effective_mismatch_not_success',(await preview.innerText()).includes('原生有效值与目标设置不同。')&&!(await preview.innerText()).includes('目标设置核对相符'));
  phase='preservation-permissions';await run('disabled');
  check('permission_difference_refuses_success',(await preview.innerText()).includes('保留核对发现变化：权限规则与工作目录')&&!(await preview.innerText()).includes('目标设置核对相符'));
  phase='preservation-other-plugin';await run('disabled');
  check('other_plugin_difference_visible',(await preview.innerText()).includes('其他有效设置（含未选择的插件）')&&!(await preview.innerText()).includes('目标设置核对相符'));
  phase='preservation-missing';await run('disabled');
  check('legacy_missing_preservation_is_unknown',(await preview.innerText()).includes('未取得原文件与临时文件的保留核对依据。')&&!(await preview.innerText()).includes('目标设置核对相符'));
  phase='retained';await run('disabled');
  check('retained_file_diagnostic_visible',(await preview.innerText()).includes('临时设置文件尚未回收')&&(await preview.innerText()).includes('派生设置文件仍在使用'));
  fail=true;await preview.getByRole('button',{name:sourceKey+'：核对排除设置',exact:true}).click();await preview.getByRole('alert').waitFor();
  check('failed_preview_clears_previous_result',await preview.getByRole('status').count()===0&&!(await preview.innerText()).includes('临时设置文件尚未回收'));
  check('stale_source_requires_reread',(await preview.innerText()).includes('重新读取安装登记'));
  fail=false;phase='disabled';await run('disabled');
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await preview.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await page.screenshot({path:output+'/preview-390.png',fullPage:true});
  await page.setViewportSize({width:1440,height:1050});
  hold=true;await preview.getByRole('button',{name:sourceKey+'：核对排除设置',exact:true}).click();await preview.getByText('正在核对设置和来源版本…',{exact:true}).waitFor();
  check('duplicate_request_disabled',await preview.getByRole('button',{name:sourceKey+'：核对启用设置',exact:true}).isDisabled());
  const nextGeneration=fixture.responses[base].session.generation+1;
  for(const path of ['/api/workspace','/api/snapshot']){fixture.responses[path].sequence+=100;fixture.responses[path].sessions[0].generation=nextGeneration;}
  fixture.responses[base].sequence+=100;fixture.responses[base].session.generation=nextGeneration;
  await page.evaluate(()=>window.previewEvents.listeners.change());
  await panel.getByRole('button',{name:'读取安装登记',exact:true}).waitFor({state:'attached'});
  await page.waitForFunction(()=>!document.body.textContent.includes('正在核对设置和来源版本…'));
  check('binding_change_clears_catalog',await panel.locator('.snapshot-asset').count()===0);
  release();hold=false;
  await panel.locator(':scope>summary').click();await read();
  check('late_result_does_not_return',await preview.getByRole('status').count()===0);
  await run('disabled');
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));
  await panel.getByText('工作台已断开，连接后可重新读取。',{exact:true}).waitFor({state:'attached'});
  await panel.locator(':scope>summary').click();
  check('disconnect_clears_preview',await panel.locator('.snapshot-asset').count()===0);
  check('disconnect_disables_read',await panel.getByRole('button',{name:'读取安装登记',exact:true}).isDisabled());
  const previews=requests.filter(r=>r.path.endsWith('/plugins/settings-preview'));
  check('only_bound_keys_mode_and_version',previews.every(r=>r.method==='POST'&&Object.keys(r.body).sort().join(',')==='mode,source_key,source_version'&&r.body.source_key===sourceKey&&r.body.source_version.length===64));
  check('no_configuration_apply_or_model_command',!requests.some(r=>r.path==='/api/commands'||r.path.endsWith('/execution/apply')));
  check('no_private_values_in_ui',!(await page.content()).includes('PRIVATE_PREVIEW_VALUE'));
  check('no_browser_errors',errors.length===0);
  return{checks,requests,errors,passed:Object.values(checks).every(Boolean),scope:'real native Handler preservation and preview responses; controlled preservation differences/missing evidence, flag mismatch, retained file, delayed reply, ownership update and disconnect'};
}
