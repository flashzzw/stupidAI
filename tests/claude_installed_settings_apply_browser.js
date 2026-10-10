// Browser acceptance uses the real Handler responses from the native suite.
// Only unavailable-receipt and legacy-format displays are controlled variants.
async page => {
  page=await page.context().newPage();
  const fixture=__FIXTURE__, failure=__FAILURE__, adoption=__ADOPTION__, assets=__ASSETS__, output=__OUTPUT__;
  const checks={},requests=[],errors=[];
  const sid=fixture.reports.before.id,base='/api/sessions/'+sid;
  let state='before',reportSet=fixture;
  const check=(name,ok)=>{checks[name]=Boolean(ok);if(!ok)throw Error(name+(errors.length?': '+errors.join('; '):''));};
  const copy=value=>JSON.parse(JSON.stringify(value));
  const frame=()=>reportSet.reports['browser_'+state];
  const call=(path,predicate)=>reportSet.reports.http_calls.find(c=>c.path===path&&predicate(c.body));
  const target=fixture.reports['disable-selected_saved'];
  const stage=call(base+'/plugins/settings-stage',body=>body.source_key==='selected@owned'&&body.mode==='disabled');
  const prepare=call('/api/commands',body=>body.command_id==='prepare-disable-selected');
  const apply=call('/api/commands',body=>body.command_id==='apply-disable-selected');
  page.setDefaultTimeout(8000);
  await page.unrouteAll({behavior:'ignoreErrors'});
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(({sid,failureSid,adoptionSid,id})=>{
    if(location.origin!=='http://127.0.0.1:9468')return;
    sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',location.hash==='#failed'?failureSid:location.hash==='#adoption'?adoptionSid:sid);
    window.nextCommandId=id;
    Object.defineProperty(crypto,'randomUUID',{configurable:true,value:()=>window.nextCommandId});
    window.EventSource=class{
      constructor(){this.listeners={};window.installedEvents=this;setTimeout(()=>this.onopen?.(),0);}
      addEventListener(name,fn){this.listeners[name]=fn;}close(){}
    };
  },{sid,failureSid:failure?.reports.before.id,adoptionSid:adoption?.reports.before.id,id:stage.body.command_id});
  await page.route('**/*',async route=>{
    const req=route.request(),path=req.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0],body=req.postData()?JSON.parse(req.postData()):null;
    requests.push({path,body,method:req.method()});
    if(path.startsWith('/favicon.'))return route.fulfill({status:204});
    if(path.endsWith('/execution/recovery-check')){
      const native=call(path,()=>true);
      check('recovery_check_uses_failed_operation',JSON.stringify(body)===JSON.stringify(native.body));
      return route.fulfill({status:native.status,json:native.response});
    }
    if(reportSet===adoption&&path.endsWith('/execution/preview')){
      const native=call(path,()=>true);
      check('local_adoption_preview_uses_original_version',body.adopt_local_settings===true&&body.base_snapshot_id===native.body.base_snapshot_id);
      return route.fulfill({status:native.status,json:native.response});
    }
    if(path===base+'/plugins/installed')return route.fulfill({json:call(path,()=>true).response});
    if(path===base+'/plugins/settings-preview')return route.fulfill({json:call(path,r=>r.source_key===body.source_key&&r.mode===body.mode).response});
    if(path===base+'/plugins/settings-stage'){
      check('save_uses_server_selection_contract',JSON.stringify(Object.keys(body).sort())===JSON.stringify(Object.keys(stage.body).sort())&&body.base_snapshot_id===stage.body.base_snapshot_id&&body.source_key===stage.body.source_key&&body.mode===stage.body.mode&&body.source_version===stage.body.source_version);
      state='disable-selected_saved';return route.fulfill({status:stage.status,json:stage.response});
    }
    if(path==='/api/commands'){
      if(body.kind==='recover_execution'){
        const native=call(path,b=>b.kind==='recover_execution');
        check('recovery_command_uses_failed_operation',body.session_id===native.body.session_id&&JSON.stringify(body.execution_recovery)===JSON.stringify(native.body.execution_recovery));
        state='recovered';return route.fulfill({status:native.status,json:native.response});
      }
      if(reportSet===adoption){
        const ids={stage_execution:'save-installed-adoption',prepare_execution:'prepare-installed-adoption',apply_execution:'apply-installed-adoption'};
        const native=call(path,b=>b.command_id===ids[body.kind]);
        check('adoption_'+body.kind+'_uses_saved_version',body.session_id===native.body.session_id&&body.execution_change.base_snapshot_id===native.body.execution_change.base_snapshot_id);
        if(body.kind==='stage_execution')check('adoption_save_keeps_preview_and_local_consent',body.execution_change.adopt_local_settings===true&&body.execution_change.preview_hash===native.body.execution_change.preview_hash);
        else check('adoption_'+body.kind+'_uses_candidate_id',body.execution_change.pending_change_id===native.body.execution_change.pending_change_id);
        state={stage_execution:'adoption_saved',prepare_execution:'adoption_prepared',apply_execution:'adoption_applied'}[body.kind];
        return route.fulfill({status:native.status,json:native.response});
      }
      const native=body.kind==='prepare_execution'?prepare:body.kind==='apply_execution'?apply:undefined;
      if(!native)return route.fulfill({status:409,json:{error:'unexpected browser mutation'}});
      check(body.kind+'_uses_original_candidate',body.session_id===sid&&body.execution_change.base_snapshot_id===target.base_snapshot_id&&body.execution_change.pending_change_id===target.id);
      state=body.kind==='prepare_execution'?'disable-selected_prepared':'disable-selected_applied';
      return route.fulfill({status:native.status,json:native.response});
    }
    if(frame()?.[path])return route.fulfill({json:frame()[path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'Unmapped installed settings acceptance request'});
  });
  const refresh=async()=>{
    const id=reportSet.reports.before.id;
    const response=frame()['/api/sessions/'+id],expected=response.session??response;
    await page.evaluate(()=>window.installedEvents.listeners.change());
    const version=expected.pending_execution_change?.snapshot.id??expected.execution_snapshot.id;
    await page.locator('.execution-snapshot').getByText(expected.pending_execution_change?'候选快照 '+version:version,{exact:true}).waitFor({state:'attached'});
    if(!expected.pending_execution_change)await page.getByRole('button',{name:'撤销待应用选择',exact:true}).waitFor({state:'detached'});
  };
  const details=async()=>{
    const root=page.locator('.execution-snapshot');await root.waitFor();
    if(await root.getAttribute('open')===null)await root.locator(':scope>summary').click();
  };
  const select=async()=>{
    const panel=page.locator('details.skill-discovery').filter({has:page.getByText('Claude 已安装插件登记',{exact:true})});
    await panel.locator(':scope>summary').click();
    await panel.getByRole('button',{name:'读取安装登记',exact:true}).click();
    await panel.getByText('支持范围内的登记采样完成 · 2 条安装记录',{exact:true}).waitFor();
    await panel.getByRole('button',{name:'selected@owned：核对排除设置',exact:true}).click();
    const button=panel.getByRole('button',{name:'selected@owned：保存待应用方案',exact:true});
    await button.waitFor();check('verified_preview_can_save',await button.isEnabled());await button.click();
  };
  await page.setViewportSize({width:1440,height:1050});
  await page.goto('http://127.0.0.1:9468/');await details();await select();await refresh();
  const pending=page.locator('.execution-change');
  await pending.getByText('待应用版本 2 · 尚未生效',{exact:true}).waitFor();
  check('saved_version_does_not_replace_current',await page.locator('.execution-snapshot').getByText(fixture.reports.before.execution_snapshot.id,{exact:true}).count()===1);
  check('new_candidate_can_prepare',await pending.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).isEnabled());
  check('ordinary_form_does_not_replace_installed_candidate',await pending.getByRole('button',{name:'预览改配差异',exact:true}).isDisabled());
  check('apply_requires_preparation',await pending.getByRole('button',{name:'应用已保存配置',exact:true}).count()===0);
  await page.evaluate(id=>{window.nextCommandId=id;},prepare.body.command_id);
  await pending.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).click();await refresh();
  const applyButton=pending.getByRole('button',{name:'应用已保存配置',exact:true});await applyButton.waitFor();
  check('prepared_candidate_can_apply',await applyButton.isEnabled());
  check('prepared_state_explains_pause',(await pending.innerText()).includes('已取得空闲依据'));
  await pending.scrollIntoViewIfNeeded();await page.screenshot({path:output+'/prepared-1440.png'});
  await page.evaluate(id=>{window.nextCommandId=id;},apply.body.command_id);
  await applyButton.click();await refresh();await details();
  const plugins=page.getByRole('region',{name:'本次 CLI 插件选择'});
  await plugins.waitFor();
  check('applied_plugin_source_visible',(await plugins.innerText()).includes('selected@owned'));
  check('applied_plugin_excluded_and_verified',(await plugins.innerText()).includes('禁用整包')&&(await plugins.innerText()).includes('原生包与组件清单匹配'));
  check('applied_version_replaces_pending',await page.getByRole('button',{name:'撤销待应用选择',exact:true}).count()===0);
  await page.getByText('本执行版本的插件设置文件',{exact:true}).click();
  check('execution_file_binding_visible',(await page.locator('.execution-snapshot').innerText()).includes(target.snapshot.claude_installed_plugin_settings.binding.artifact.file.sha256));
  state='enable-selected_applied';await page.reload();await details();await plugins.waitFor();
  const selected=plugins.locator('.snapshot-asset').filter({has:page.getByText('来源键：selected@owned',{exact:true})});
  const sibling=plugins.locator('.snapshot-asset').filter({has:page.getByText('来源键：sibling@owned',{exact:true})});
  check('multiple_installed_sources_display_separately',await selected.count()===1&&await sibling.count()===1);
  check('next_change_keeps_previous_choice',(await selected.innerText()).includes('启用整包')&&(await sibling.innerText()).includes('禁用整包'));
  await plugins.scrollIntoViewIfNeeded();await page.screenshot({path:output+'/applied-1440.png'});
  await page.setViewportSize({width:390,height:844});
  await page.getByRole('button',{name:'详情',exact:true}).click();await plugins.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await page.screenshot({path:output+'/applied-390.png'});
  await page.setViewportSize({width:1440,height:1050});
  const partial=copy(frame());
  for(const response of Object.values(partial)){
    for(const s of response.sessions??(response.session?[response.session]:[])){
      if(s.id===sid){const p=s.view?.plugin_evidence?.find(e=>e.native_source==='sibling@owned');if(p){p.verified=false;p.detail='此来源的加载回执暂不可用';}}
    }
  }
  fixture.reports.browser_partial=partial;state='partial';await page.reload();await details();await plugins.waitFor();
  check('unavailable_sibling_does_not_reuse_first_receipt',(await selected.innerText()).includes('原生包与组件清单匹配')&&(await sibling.innerText()).includes('原生包与组件待核对'));
  const legacy=copy(fixture.reports['browser_disable-selected_saved']);
  for(const response of Object.values(legacy))for(const s of response.sessions??(response.session?[response.session]:[])){
    if(s.id===sid&&s.pending_execution_change)delete s.pending_execution_change.snapshot.claude_installed_plugin_settings.binding;
  }
  fixture.reports.browser_legacy=legacy;state='legacy';await page.reload();await details();
  await pending.getByText('旧候选缺少执行文件绑定；请撤销后重新核对保存。',{exact:true}).waitFor();
  check('legacy_candidate_remains_non_executable',await pending.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).isDisabled());
  if(failure){
    reportSet=failure;state='failed_after_restart';
    await page.goto('http://127.0.0.1:9468/#failed');await page.reload();await details();
    await page.getByText('启动候选配置失败；实际配置仍待核对，不自动回滚或重放。',{exact:true}).waitFor();
    check('failed_candidate_stays_unknown',(await pending.innerText()).includes('实际生效待核对'));
    check('failed_candidate_has_no_apply_button',await pending.getByRole('button',{name:'应用已保存配置',exact:true}).count()===0);
    check('failed_candidate_cannot_be_discarded',await pending.getByRole('button',{name:'撤销待应用选择',exact:true}).isDisabled());
    await pending.scrollIntoViewIfNeeded();await page.screenshot({path:output+'/unknown-1440.png'});
    if(failure.reports.recovered){
      await pending.getByRole('button',{name:'核对恢复条件',exact:true}).click();
      const recover=pending.getByRole('button',{name:'按原配置恢复查询',exact:true});await recover.waitFor();
      check('restored_sources_allow_explicit_recovery',await recover.isEnabled());
      await page.evaluate(id=>{window.nextCommandId=id;},call('/api/commands',b=>b.kind==='recover_execution').body.command_id);
      await recover.click();await refresh();await details();
      await page.getByRole('region',{name:'未生效的配置尝试'}).waitFor();
      check('recovery_restores_original_snapshot_display',await page.locator('.execution-snapshot').getByText(failure.reports.recovered.execution_snapshot.id,{exact:true}).count()===1);
      check('recovery_archives_candidate_instead_of_reapplying',await pending.getByRole('button',{name:'应用已保存配置',exact:true}).count()===0&&await page.getByRole('region',{name:'未生效的配置尝试'}).count()===1);
      await page.locator('.execution-snapshot').scrollIntoViewIfNeeded();await page.screenshot({path:output+'/recovered-1440.png'});
    }
  }
  if(adoption){
    reportSet=adoption;state='disable-selected_applied';
    await page.goto('http://127.0.0.1:9468/#adoption');await page.reload();await details();
    await pending.getByLabel('采纳当前项目/本地设置版本',{exact:true}).check();
    await pending.getByRole('button',{name:'预览改配差异',exact:true}).click();
    const save=pending.getByRole('button',{name:'保存待应用选择',exact:true});await save.waitFor();
    await page.evaluate(id=>{window.nextCommandId=id;},'save-installed-adoption');await save.click();await refresh();
    check('adoption_preserves_installed_choices_in_pending_view',(await pending.innerText()).includes('selected@owned（禁用）')||(await pending.innerText()).includes('selected@owned（禁用整包）'));
    await page.evaluate(id=>{window.nextCommandId=id;},'prepare-installed-adoption');
    await pending.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).click();await refresh();
    const apply=pending.getByRole('button',{name:'应用已保存配置',exact:true});await apply.waitFor();
    await page.evaluate(id=>{window.nextCommandId=id;},'apply-installed-adoption');await apply.click();await refresh();await details();
    const actual=adoption.reports.adoption_applied;
    check('adoption_promotes_new_version_in_view',await page.locator('.execution-snapshot').getByText(actual.execution_snapshot.id,{exact:true}).count()===1);
    await page.getByText('本执行版本的插件设置文件',{exact:true}).click();
    check('adoption_displays_current_startup_source',(await page.locator('.execution-snapshot').innerText()).includes(actual.scope.claude_installed_settings.startup_source_version));
    await page.locator('.execution-snapshot').scrollIntoViewIfNeeded();await page.screenshot({path:output+'/adopted-1440.png'});
  }
  const expected=['prepare_execution','apply_execution',...(failure?.reports.recovered?['recover_execution']:[]),...(adoption?['stage_execution','prepare_execution','apply_execution']:[])];
  check('browser_only_dispatched_explicit_user_actions',requests.filter(r=>r.path==='/api/commands').map(r=>r.body.kind).join(',')===expected.join(','));
  check('configuration_refresh_keeps_one_catalog',await page.getByText('Claude Skill 来源清单',{exact:true}).count()===1);
  check('configuration_refresh_keeps_one_source_check',await page.getByRole('button',{name:'核对当前来源',exact:true}).count()===1);
  check('no_browser_exceptions',errors.length===0);
  await page.close();
  return {checks,requests,errors,passed:Object.values(checks).every(Boolean)};
}
