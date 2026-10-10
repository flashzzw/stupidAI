// Actual Handler snapshots and preview; command/SSE responses are replayed.
async page => {
  const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
  const sid=fixture.samples.initial.id,detail='/api/sessions/'+sid;
  let phase='drifted',sequence=1000;
  const clone=v=>JSON.parse(JSON.stringify(v));
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  page.setDefaultTimeout(5000);await page.unrouteAll({behavior:'ignoreErrors'});
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{
    constructor(){this.listeners={};window.configurationAdoptionEvents=this;setTimeout(()=>this.onopen?.(),0);}addEventListener(name,fn){this.listeners[name]=fn;}close(){}
  };},sid);
  await page.route('**/*',async route=>{
    const req=route.request(),path=req.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=req.postData()?JSON.parse(req.postData()):undefined;
    requests.push({path,method:req.method(),body});
    if(path===detail+'/execution/preview')return route.fulfill({json:fixture.samples.preview});
    if(path==='/api/commands'){
      const next={stage_execution:'staged',prepare_execution:'prepared',apply_execution:'applied'}[body.kind];
      if(!next)return route.fulfill({status:409,json:{error:'unexpected fixture command'}});
      phase=next;sequence+=100;
      await route.fulfill({json:{command_id:body.command_id,session_id:sid,status:body.kind==='stage_execution'?'pending':'accepted'}});
      await page.evaluate(()=>window.configurationAdoptionEvents.listeners.change?.());return;
    }
    if(fixture.responses[phase][path]){const row=clone(fixture.responses[phase][path]);row.sequence=sequence;return route.fulfill({json:row});}
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    if(path==='/favicon.ico')return route.fulfill({status:204,body:''});
    return route.fulfill({status:404,body:'unmapped fixture path'});
  });
  await page.setViewportSize({width:1440,height:1000});await page.goto('http://127.0.0.1:9463/');
  const snapshot=page.locator('.execution-snapshot');await snapshot.locator(':scope > summary').click();
  const panel=page.getByRole('region',{name:'原会话配置改配',exact:true});
  const choice=panel.getByRole('checkbox',{name:'采纳当前配置来源版本',exact:true});
  check('explicit_choice',!await choice.isChecked());
  check('empty_preview_disabled',await panel.getByRole('button',{name:'预览改配差异',exact:true}).isDisabled());
  check('input_stays_blocked',await page.getByRole('button',{name:'发送消息',exact:true}).isDisabled());
  await choice.check();await panel.getByRole('button',{name:'预览改配差异',exact:true}).click();
  const diff=panel.getByRole('region',{name:'待采纳配置来源',exact:true}),change=fixture.samples.preview.codex_configuration_change.files[0];
  await diff.waitFor();
  const text=await diff.innerText();
  check('both_versions_visible',text.includes(change.before.sha256)&&text.includes(change.after.sha256));
  check('source_path_visible',text.includes(change.before.path));
  check('effort_before_after_visible',text.includes('low → high'));
  check('other_settings_preserved',text.includes('其他设置、hook 文件及来源身份保持原版本'));
  check('no_simultaneous_hook_adoption',!await panel.getByRole('checkbox',{name:'采纳当前用户/项目 hook 文件版本',exact:true}).isChecked());
  check('trust_not_automatic',(await panel.innerText()).includes('不自动信任新 hash'));
  check('no_hook_command_disclosed',!text.includes('marker.py')&&!text.includes('os.execl'));
  await diff.scrollIntoViewIfNeeded();await page.screenshot({path:'.dev/w0209-config-apply/preview-1440.png',fullPage:true});
  await panel.getByRole('button',{name:'保存待应用选择',exact:true}).click();
  await panel.getByText('待应用版本 2 · 尚未生效',{exact:true}).waitFor();
  check('staging_keeps_original_id',(await snapshot.innerText()).includes(fixture.samples.initial.execution_snapshot.id));
  check('prepare_available_for_source_drift',await panel.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).isEnabled());
  await panel.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).click();
  await panel.getByRole('button',{name:'应用已保存配置',exact:true}).waitFor();
  check('prepare_keeps_original_id',(await snapshot.innerText()).includes(fixture.samples.initial.execution_snapshot.id));
  check('explicit_load_boundary',(await panel.innerText()).includes('复核加载状态'));
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await panel.scrollIntoViewIfNeeded();
  check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await page.screenshot({path:'.dev/w0209-config-apply/prepared-390.png',fullPage:true});
  await panel.getByRole('button',{name:'应用已保存配置',exact:true}).click();
  await snapshot.getByText(fixture.samples.applied.execution_snapshot.id,{exact:true}).waitFor();
  check('new_snapshot_visible',fixture.samples.applied.execution_snapshot.id!==fixture.samples.initial.execution_snapshot.id);
  check('same_native_identity',fixture.samples.applied.scope.native_id===fixture.samples.initial.scope.native_id);
  check('pending_cleared',!(await panel.innerText()).includes('待应用版本 2'));
  check('next_adoption_requires_choice',!await choice.isChecked());
  const commands=requests.filter(r=>r.path==='/api/commands');
  check('only_three_expected_commands',commands.map(r=>r.body.kind).join(',')==='stage_execution,prepare_execution,apply_execution');
  check('reviewed_hash_bound',commands[0].body.execution_change.adopt_codex_configuration_sources===true&&commands[0].body.execution_change.preview_hash===fixture.samples.preview.preview_hash);
  check('apply_only_saved_reference',commands.slice(1).every(r=>r.body.execution_change.pending_change_id===fixture.samples.staged.pending_execution_change.id&&!r.body.execution_change.adopt_codex_configuration_sources));
  check('no_input_commands',!commands.some(r=>['send','respond'].includes(r.body.kind)));
  check('no_page_errors',errors.length===0);
  return {checks,passed:Object.values(checks).every(Boolean),requests,errors,scope:'actual native Handler responses; browser command/SSE replay only, no new native execution'};
}
