// Actual cold recovery Handler snapshots; HTTP commands/SSE are replayed.
async page => {
 const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
 const original=fixture.samples.retire_staged,sid=original.id,report=fixture.samples.cold_recovery_check;
 let phase='cold_loss_reopened',sequence=1000,release;
 const check=(name,value)=>{checks[name]=!!value;if(!value)throw Error(name);};
 page.setDefaultTimeout(6000);await page.unrouteAll({behavior:'ignoreErrors'});
 await page.addInitScript(id=>{sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{
  constructor(){this.listeners={};window.coldRecoveryEvents=this;setTimeout(()=>this.onopen?.(),0);}addEventListener(name,fn){this.listeners[name]=fn;}close(){}
 };},sid);
 page.on('pageerror',e=>errors.push(e.message));
 await page.route('**/*',async route=>{
  const req=route.request(),path=req.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=req.postData()?JSON.parse(req.postData()):undefined;
  requests.push({path,method:req.method(),body});
  if(path==='/api/sessions/'+sid+'/execution/recovery-check')return route.fulfill({json:report});
  if(path==='/api/commands'){
   if(body.kind!=='recover_execution')return route.fulfill({status:409,json:{error:'unexpected fixture command'}});
   await new Promise(resolve=>release=resolve);phase='cold_recovered';sequence+=100;
   await route.fulfill({json:{command_id:body.command_id,session_id:sid,status:'accepted'}});
   await page.evaluate(()=>window.coldRecoveryEvents.listeners.change?.());return;
  }
  if(fixture.responses[phase]?.[path]){const row=JSON.parse(JSON.stringify(fixture.responses[phase][path]));row.sequence=sequence;return route.fulfill({json:row});}
  if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
  if(path==='/favicon.ico')return route.fulfill({status:204,body:''});
  return route.fulfill({status:404,body:'unmapped fixture path'});
 });
 await page.setViewportSize({width:1440,height:1000});await page.goto('http://127.0.0.1:9463/');
 const snapshot=page.locator('.execution-snapshot');await snapshot.locator(':scope > summary').click();
 const panel=page.getByRole('region',{name:'恢复前只读核对',exact:true});await panel.waitFor();
 const probe=panel.getByRole('button',{name:'核对恢复条件',exact:true});
 check('no_automatic_probe',!requests.some(r=>r.path.endsWith('/recovery-check')));
 check('no_unchecked_recovery',await panel.getByRole('button',{name:'按原配置恢复原线程',exact:true}).count()===0);
 check('unknown_input_blocked',await page.getByRole('button',{name:'发送消息',exact:true}).isDisabled());
 check('old_snapshot_visible',(await snapshot.innerText()).includes(original.execution_snapshot.id));
 await probe.click();const recover=panel.getByRole('button',{name:'按原配置恢复原线程',exact:true});await recover.waitFor();
 check('native_scope_checks_visible',(await panel.innerText()).includes('原线程与加载清单均确认未加载：已核对')&&(await panel.innerText()).includes('原生历史与原基线一致：已核对'));
 check('original_sources_passed',(await panel.innerText()).includes('已记录的原配置来源均匹配。'));
 const queried=requests.find(r=>r.path.endsWith('/recovery-check')).body;
 check('check_binds_failed_operation',queried.base_snapshot_id===report.base_snapshot_id&&queried.pending_change_id===report.pending_change_id&&queried.operation_id===report.native.operation);
 await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await panel.scrollIntoViewIfNeeded();
 check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 await page.screenshot({path:'.dev/w0209-codex-cold-recovery/ready-390.png',fullPage:true});
 await recover.click();for(let i=0;i<100&&!release;i++)await page.waitForTimeout(10);if(!release)throw Error('recovery command not dispatched');
 check('no_duplicate_recovery_button',await panel.getByRole('button',{name:'按原配置恢复原线程',exact:true}).count()===0);
 const command=requests.find(r=>r.path==='/api/commands').body;
 check('command_binds_same_original',command.session_id===sid&&JSON.stringify(command.execution_recovery)===JSON.stringify(queried));
 release();const archive=page.getByRole('region',{name:'未生效的配置尝试',exact:true});await archive.waitFor();
 await archive.locator(':scope > details > summary').click();
 check('original_snapshot_retained',await snapshot.getByText(original.execution_snapshot.id,{exact:true}).count()===1);
 check('failed_candidate_archived',(await archive.innerText()).includes(original.pending_execution_change.snapshot.id));
 check('failure_reason_retained',(await archive.innerText()).includes('原生改配未取得完整确认'));
 check('automation_stays_paused',await page.getByRole('button',{name:'恢复自动化',exact:true}).count()===1);
 check('recovery_panel_cleared',await panel.count()===0);
 const plugins=page.getByRole('region',{name:'本次 CLI 插件选择',exact:true});
 check('original_hook_trust_visible',(await plugins.innerText()).includes('本版本 hook：已明确信任'));
 const pluginText=await plugins.innerText();check('original_plugin_source_visible',pluginText.includes(original.execution_snapshot.plugin_selections.find(pack=>pack.asset_id==='bundle').native_source));
 const skill=original.execution_snapshot.capability_refs.find(ref=>ref.asset.id==='loose').asset;check('original_skill_asset_visible',(await page.getByRole('region',{name:'本次原生 Skill 选择',exact:true}).innerText()).includes(skill.name+' · 启用'));
 await archive.scrollIntoViewIfNeeded();await page.screenshot({path:'.dev/w0209-codex-cold-recovery/recovered-390.png',fullPage:true});
 check('one_explicit_command',requests.filter(r=>r.path==='/api/commands').length===1);
 check('no_model_input_commands',!requests.some(r=>r.path==='/api/commands'&&['send','respond','resume'].includes(r.body.kind)));
 check('no_page_errors',errors.length===0);
 return {checks,passed:Object.values(checks).every(Boolean),requests,errors,scope:'real native final-reply-loss recovery Handler snapshots; browser commands/SSE replay only'};
}
