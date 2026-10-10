// Real configuration adoption failure/recovery Handler states, replayed HTTP/SSE.
async page => {
 const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[],clone=x=>JSON.parse(JSON.stringify(x));
 const sid=fixture.samples.initial.id,detail='/api/sessions/'+sid;
 const originalEffort=fixture.samples.initial.execution_snapshot.codex_configuration_sources.settings.reasoning_effort.value;
 const targetEffort=fixture.samples.staged.pending_execution_change.snapshot.codex_configuration_sources.settings.reasoning_effort.value;
 let phase='uncertain',blocked=true,sequence=2000;
 const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
 page.setDefaultTimeout(6000);await page.unrouteAll({behavior:'ignoreErrors'});page.on('pageerror',e=>errors.push(e.message));
 await page.addInitScript(id=>{sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){this.listeners={};window.configRecoveryEvents=this;setTimeout(()=>this.onopen?.(),0);}addEventListener(k,f){this.listeners[k]=f;}close(){}};},sid);
 await page.route('**/*',async route=>{
  const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=r.postData()?JSON.parse(r.postData()):undefined;requests.push({path,method:r.method(),body});
  if(path===detail+'/execution/recovery-check'){
   const data=clone(fixture.samples.recovery_check);
   if(blocked){data.satisfied=false;data.sources[0].status='changed';data.sources[0].detail='原配置来源文件尚未还原';}
   return route.fulfill({json:data});
  }
  if(path==='/api/commands'){
   if(body.kind!=='recover_execution')return route.fulfill({status:409,body:'Unexpected command'});
   phase='recovered';sequence+=100;
   await route.fulfill({json:{command_id:body.command_id,session_id:sid,status:'accepted'}});
   await page.evaluate(()=>window.configRecoveryEvents.listeners.change?.());return;
  }
  if(fixture.responses[phase][path]){const data=clone(fixture.responses[phase][path]);data.sequence=sequence;return route.fulfill({json:data});}
  if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
  return route.fulfill({status:path.startsWith('/favicon')?204:404,body:''});
 });
 await page.setViewportSize({width:1440,height:1000});await page.goto('http://127.0.0.1:9463/');await page.locator('.execution-snapshot > summary').click();
 const snapshot=page.locator('.execution-snapshot'),changes=page.locator('.execution-change'),recovery=page.getByRole('region',{name:'恢复前只读核对'});
 check('old_snapshot_on_uncertain',(await snapshot.innerText()).includes(fixture.samples.initial.execution_snapshot.id));
 check('candidate_effort_visible',(await changes.getByRole('region',{name:'待采纳配置来源'}).innerText()).includes(originalEffort+' → '+targetEffort));
 check('unknown_not_success',(await changes.innerText()).includes('实际生效待核对'));
 check('no_automatic_recovery_request',requests.every(r=>!r.path.endsWith('/recovery-check')&&r.path!=='/api/commands'));
 check('input_disabled',await page.getByRole('button',{name:'发送消息',exact:true}).isDisabled());
 await recovery.getByRole('button',{name:'核对恢复条件',exact:true}).click();await recovery.getByText('还有条件未满足，保持待核对。',{exact:true}).waitFor();
 check('original_file_required',await recovery.getByRole('button',{name:'按原配置恢复原线程',exact:true}).count()===0);
 blocked=false;await recovery.getByRole('button',{name:'核对恢复条件',exact:true}).click();const restore=recovery.getByRole('button',{name:'按原配置恢复原线程',exact:true});await restore.waitFor();
 await restore.click();const attempts=page.getByRole('region',{name:'未生效的配置尝试'});await attempts.waitFor();
 await attempts.locator(':scope > details > summary').click();
 check('failed_candidate_archived',(await attempts.innerText()).includes(fixture.samples.staged.pending_execution_change.snapshot.id));
 check('original_snapshot_retained',(await snapshot.innerText()).includes(fixture.samples.initial.execution_snapshot.id));
 check('automation_remains_paused',await page.getByRole('button',{name:'恢复自动化',exact:true}).count()===1);
 check('recovery_panel_removed',await recovery.count()===0);
 const saved=page.locator('.configuration-source-snapshot');await saved.first().locator(':scope > summary').click();
 check('original_effort_recorded',(await saved.first().innerText()).includes('reasoning effort：'+originalEffort));
 if(fixture.samples.initial.scope.plugin_selections?.length===2){
  const plugins=page.getByRole('region',{name:'本次 CLI 插件选择'});
  const skills=page.getByRole('region',{name:'本次原生 Skill 选择'});
  check('mixed_original_packages_restored',await plugins.locator('.snapshot-asset').count()===1&&(await skills.innerText()).includes('Owned independent Skill · 启用'));
  check('mixed_original_hook_configuration_verified',(await plugins.innerText()).includes('本连接 hook 配置已核对'));
 }
 const commands=requests.filter(r=>r.path==='/api/commands');
 check('one_bound_recovery',commands.length===1&&commands[0].body.execution_recovery.base_snapshot_id===fixture.samples.initial.execution_snapshot.id&&commands[0].body.execution_recovery.operation_id===fixture.samples.apply_receipt.operation_id);
 check('no_page_errors',errors.length===0);
 await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await attempts.scrollIntoViewIfNeeded();
 check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:(fixture.screenshot_directory??'.dev/w0209-config-apply')+'/recovered-390.png',fullPage:true});
 return {checks,passed:Object.values(checks).every(Boolean),requests,errors,scope:'real native configuration failure and recovery responses; synthetic original-file-unavailable check, browser commands replayed'};
}
