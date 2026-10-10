// Replay real Handler states before and after a second lost native reply.
async page => {
 const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
 const original=fixture.samples.retire_prepared,sid=original.id;
 let phase='recovery_uncertain';
 const check=(name,value)=>{checks[name]=!!value;if(!value)throw Error(name);};
 page.setDefaultTimeout(6000);await page.unrouteAll({behavior:'ignoreErrors'});
 await page.addInitScript(id=>{sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){this.listeners={};setTimeout(()=>this.onopen?.(),0);}addEventListener(name,fn){this.listeners[name]=fn;}close(){}};},sid);
 page.on('pageerror',e=>errors.push(e.message));
 await page.route('**/*',async route=>{
  const req=route.request(),path=req.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=req.postData()?JSON.parse(req.postData()):undefined;
  requests.push({path,method:req.method(),body});
  if(fixture.responses[phase]?.[path])return route.fulfill({json:fixture.responses[phase][path]});
  if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
  if(path==='/favicon.ico')return route.fulfill({status:204,body:''});
  return route.fulfill({status:409,body:'no mutation permitted in this replay'});
 });
 const inspect=async prefix=>{
  const state=fixture.samples[phase],pending=state.pending_execution_change;
  const snapshot=page.locator('.execution-snapshot');await snapshot.locator(':scope > summary').click();
  const message=page.getByText('按原配置恢复中 · 结果待核对，自动化仍暂停；不会重复启动。',{exact:true});await message.waitFor();
  check(prefix+'_unknown_explained',await message.isVisible());
  check(prefix+'_old_snapshot_visible',(await snapshot.innerText()).includes(original.execution_snapshot.id));
  check(prefix+'_pending_candidate_visible',(await snapshot.innerText()).includes(pending.snapshot.id));
  check(prefix+'_no_success_archive',await page.getByRole('region',{name:'未生效的配置尝试',exact:true}).count()===0);
  check(prefix+'_no_confirmed_banner',await page.getByText('原配置恢复已确认；原线程当前未加载，能力仍待核对。请明确恢复自动化后继续。',{exact:true}).count()===0);
  check(prefix+'_no_recovery_retry',await page.getByRole('button',{name:'按原配置恢复原线程',exact:true}).count()===0);
  check(prefix+'_no_new_recovery_check',await page.getByRole('button',{name:'核对恢复条件',exact:true}).count()===0);
  check(prefix+'_no_apply',await page.getByRole('button',{name:'应用已保存配置',exact:true}).count()===0);
  await page.getByRole('textbox',{name:'消息',exact:true}).fill('Owned draft; must not send');
  check(prefix+'_input_disabled',await page.getByRole('button',{name:'发送消息',exact:true}).isDisabled());
  check(prefix+'_automation_resume_disabled',await page.getByRole('button',{name:'恢复自动化',exact:true}).isDisabled());
  const change=page.locator('.execution-change');check(prefix+'_discard_disabled',await change.getByRole('button',{name:'撤销待应用选择',exact:true}).isDisabled());
  const operation=page.locator('.operation').filter({has:page.getByText(pending.recovery_operation_id,{exact:true})});await operation.locator(':scope > summary').click();
  check(prefix+'_recovery_id_visible',(await operation.innerText()).includes(pending.recovery_operation_id));
  check(prefix+'_separate_failed_apply_id',pending.operation_id!==pending.recovery_operation_id&&await page.locator('.operation').filter({has:page.getByText(pending.operation_id,{exact:true})}).count()===1);
  check(prefix+'_no_ack_claim',(await operation.innerText()).includes('已保存的原配置恢复确认')===false);
 };
 await page.setViewportSize({width:1440,height:1000});await page.goto('http://127.0.0.1:9463/');await inspect('before');
 phase='recovery_loss_reopened';await page.reload();await inspect('reopened');
 await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await page.locator('.execution-change').scrollIntoViewIfNeeded();
 check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 await page.screenshot({path:'.dev/w0209-codex-recovery-loss/unknown-390.png',fullPage:true});
 check('no_mutations_or_automatic_checks',requests.every(r=>r.method==='GET'));
 check('no_page_errors',errors.length===0);
 return {checks,passed:Object.values(checks).every(Boolean),requests,errors,scope:'real final recovery reply loss and restarted Handler states; browser read-only replay'};
}
