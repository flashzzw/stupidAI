// Replay real Application/Handler snapshots. Lost HTTP replies and incomplete
// preview evidence are controlled browser cases, not additional native runs.
async page => {
  const fixture=__FIXTURE__,assets=__ASSETS__,output=__OUTPUT__;
  const checks={},requests=[],errors=[];
  const sid=fixture.results.session_id,base='/api/sessions/'+sid;
  const candidate=fixture.results.candidate_change,receipt=fixture.results.candidate_receipt;
  const sourceKey=candidate.snapshot.claude_installed_plugin_settings.preview.setting.source_key;
  const originalId=candidate.base_snapshot_id;
  let state='candidate_before',previewCase='good',stageCount=0,release;
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  const copy=value=>JSON.parse(JSON.stringify(value));
  const previewReport=()=>{
    const report=copy(fixture.results.candidate_preview);
    if(previewCase==='missing-startup')delete report.startup_source_version;
    if(previewCase==='missing-preservation')delete report.validation.preservation;
    if(previewCase==='unstable')report.sources_stable=false;
    if(previewCase==='target'){report.validation.plugin_settings[0].matches=false;report.validation.plugin_settings[0].actual_enabled=true;}
    if(previewCase==='schema')report.validation.schema_valid=false;
    if(previewCase==='retained')report.cleanup.state='retained';
    return report;
  };
  page.setDefaultTimeout(7000);
  await page.unrouteAll({behavior:'ignoreErrors'});
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(({sid,commandId})=>{
    if(!sessionStorage.getItem('candidate-browser-initialized')){
      sessionStorage.clear();sessionStorage.setItem('candidate-browser-initialized','1');
      sessionStorage.setItem('netcap.selected.v1',sid);
    }
    window.generatedCommandIds=0;
    Object.defineProperty(crypto,'randomUUID',{value:()=>{window.generatedCommandIds++;return window.nextCommandId||commandId;}});
    window.EventSource=class{constructor(){this.listeners={};window.candidateEvents=this;setTimeout(()=>this.onopen?.(),0);}addEventListener(name,fn){this.listeners[name]=fn;}close(){}};
  },{sid,commandId:receipt.command_id});
  await page.route('**/*',async route=>{
    const request=route.request(),path=request.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];
    const body=request.postData()?JSON.parse(request.postData()):null;
    requests.push({path,method:request.method(),body});
    if(path.startsWith('/favicon.'))return route.fulfill({status:204});
    if(path===base+'/plugins/installed')return route.fulfill({json:fixture.results.catalog});
    if(path===base+'/plugins/settings-preview')return route.fulfill({json:previewReport()});
    if(path===base+'/plugins/settings-stage'){
      stageCount++;
      if(stageCount===1){await new Promise(resolve=>{release=resolve;});return route.abort('failed');}
      if(stageCount!==2)return route.fulfill({status:409,json:{error:'unexpected extra save'}});
      state='candidate_saved';return route.fulfill({json:receipt});
    }
    if(path==='/api/commands'){
      const actual=fixture.results.candidate_http_calls.find(row=>row.body?.command_id===body.command_id&&row.body?.kind===body.kind);
      if(!actual)return route.fulfill({status:409,json:{error:'unmapped command'}});
      if(body.kind==='discard_execution')state='candidate_discarded';
      return route.fulfill({status:actual.status,json:actual.response});
    }
    if(fixture.results[state][path])return route.fulfill({json:fixture.results[state][path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'Unmapped candidate fixture request'});
  });
  const openDetails=async()=>{
    await page.locator('.execution-snapshot>summary').click();
    await page.getByText('Claude 已安装插件登记',{exact:true}).waitFor();
  };
  const panel=page.locator('details.skill-discovery').filter({has:page.getByText('Claude 已安装插件登记',{exact:true})});
  const preview=panel.locator('.snapshot-asset>.startup-settings');
  const save=preview.getByRole('button',{name:sourceKey+'：保存待应用方案',exact:true});
  const catalog=async()=>{
    await panel.locator(':scope>summary').click();
    await panel.getByRole('button',{name:'读取安装登记',exact:true}).click();
    await panel.getByText('支持范围内的登记采样完成 · 1 条安装记录',{exact:true}).waitFor();
  };
  const inspect=async()=>{
    await preview.getByRole('button',{name:sourceKey+'：核对排除设置',exact:true}).click();
    await preview.getByText('核对时间：',{exact:false}).waitFor();
  };
  await page.setViewportSize({width:1440,height:1050});
  await page.goto('http://127.0.0.1:9463/');
  await openDetails();await catalog();
  check('catalog_does_not_save',stageCount===0&&await save.count()===0);
  for(const name of ['missing-startup','missing-preservation','unstable','target','schema','retained']){
    previewCase=name;await inspect();check(name+'_cannot_save',await save.isDisabled());
  }
  previewCase='good';await inspect();
  check('good_native_observation_can_save',await save.isEnabled());
  check('no_automatic_mutation_after_preview',stageCount===0);
  check('saving_limitation_visible',(await preview.innerText()).includes('候选暂不能准备或应用'));
  await save.click();
  await preview.getByText('正在重新核对并保存…',{exact:true}).waitFor();
  check('duplicate_save_disabled',await save.isDisabled());
  check('preview_disabled_during_save',await preview.getByRole('button',{name:sourceKey+'：核对启用设置',exact:true}).isDisabled());
  await page.waitForFunction(()=>window.generatedCommandIds===1);
  check('one_native_save_request_in_flight',stageCount===1);
  release();
  await preview.getByRole('alert').waitFor();
  check('lost_reply_does_not_claim_saved',await preview.getByText('待应用方案已保存，尚未生效。可在下方查看依据或撤销。',{exact:true}).count()===0);
  check('lost_reply_remains_retryable',await save.isEnabled());
  check('lost_reply_keeps_request_id',await page.evaluate(id=>Object.entries(sessionStorage).some(([key,value])=>key.startsWith('netcap.plugin-settings-stage.v1/')&&value===id),receipt.command_id));
  await page.reload();await openDetails();await catalog();await inspect();
  await save.click();
  await preview.getByText('待应用方案已保存，尚未生效。可在下方查看依据或撤销。',{exact:true}).waitFor();
  check('reload_retry_does_not_allocate_another_id',await page.evaluate(()=>window.generatedCommandIds===0));
  const saves=requests.filter(row=>row.path.endsWith('/plugins/settings-stage'));
  check('same_request_after_lost_reply_and_reload',saves.length===2&&JSON.stringify(saves[0].body)===JSON.stringify(saves[1].body));
  check('save_only_sends_selection_and_base_ids',saves.every(row=>Object.keys(row.body).sort().join(',')==='base_snapshot_id,command_id,mode,source_key,source_version'&&row.body.source_key===sourceKey&&row.body.base_snapshot_id===originalId&&row.body.mode==='disabled'));
  await page.evaluate(()=>window.candidateEvents.listeners.change());
  const pending=page.getByRole('region',{name:'已安装插件候选依据'});
  await pending.waitFor();
  check('candidate_visible_and_not_effective',(await page.locator('.execution-change').innerText()).includes('待应用版本 2 · 尚未生效'));
  check('candidate_source_key_visible',(await page.locator('.execution-change').innerText()).includes(sourceKey+' · 已安装 CLI 插件'));
  check('candidate_preservation_and_cleanup_visible',(await pending.innerText()).includes('保存时的其他设置与权限规则保留核对一致')&&(await pending.innerText()).includes('临时设置文件已回收'));
  check('prepare_disabled_for_candidate',await page.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).isDisabled());
  check('apply_not_available',await page.getByRole('button',{name:'应用已保存配置',exact:true}).count()===0);
  check('ordinary_form_cannot_replace_candidate',await page.getByRole('button',{name:'预览改配差异',exact:true}).isDisabled());
  check('candidate_can_be_discarded',await page.getByRole('button',{name:'撤销待应用选择',exact:true}).isEnabled());
  check('other_save_cannot_overwrite_pending',await save.isDisabled());
  await pending.getByText('保存的来源版本',{exact:true}).click();
  const content=await pending.innerText(),proof=candidate.snapshot.claude_installed_plugin_settings;
  check('saved_registration_startup_resource_versions_visible',[proof.preview.source_version,proof.preview.startup_source_version,proof.declaration_resource.sha256,proof.declaration_resource.path].every(value=>content.includes(value)));
  await pending.scrollIntoViewIfNeeded();await page.screenshot({path:output+'/candidate-1440.png'});
  await page.setViewportSize({width:390,height:844});
  await page.getByRole('button',{name:'详情',exact:true}).click();
  await pending.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await page.screenshot({path:output+'/candidate-390.png'});
  await page.setViewportSize({width:1440,height:1050});
  state='candidate_restarted';await page.reload();await openDetails();await pending.waitFor();
  check('restart_restores_candidate_id',(await page.locator('.execution-change').innerText()).includes(candidate.snapshot.id));
  check('restart_preserves_original_native_identity',(await page.locator('.inspector').innerText()).includes(fixture.results.candidate_before[base].session.scope.native_id));
  check('restart_does_not_save_again',stageCount===2);
  await page.evaluate(()=>{window.nextCommandId='discard-candidate';});
  await page.getByRole('button',{name:'撤销待应用选择',exact:true}).click();
  await page.getByText('待应用选择已撤销',{exact:true}).waitFor();
  await page.evaluate(()=>window.candidateEvents.listeners.change());
  await pending.waitFor({state:'detached'});
  check('discard_removes_only_pending_display',await page.getByRole('button',{name:'撤销待应用选择',exact:true}).count()===0&&(await page.locator('.execution-snapshot').innerText()).includes(originalId));
  check('discard_uses_existing_base_and_pending_ids',requests.some(row=>row.path==='/api/commands'&&row.body.kind==='discard_execution'&&row.body.execution_change.base_snapshot_id===originalId&&row.body.execution_change.pending_change_id===candidate.id));
  check('no_prepare_apply_or_model_request',requests.filter(row=>row.path==='/api/commands').every(row=>row.body.kind==='discard_execution')&&stageCount===2);
  check('no_private_values_in_browser',!(await page.content()).includes('PRIVATE_PREVIEW_VALUE'));
  check('no_browser_exceptions',errors.length===0);
  return {checks,requests,errors,passed:Object.values(checks).every(Boolean),scope:'Actual native Application/Handler saved/restarted/discarded snapshots; controlled lost reply and preview evidence cases. Browser does not run an additional native query.'};
}
