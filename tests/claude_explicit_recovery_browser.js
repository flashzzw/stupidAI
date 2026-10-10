// Application payloads from real native recovery; transport/control responses replayed.
async page=>{
 const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
 const clone=v=>JSON.parse(JSON.stringify(v)),sid=fixture.sid;
 let responses=clone(fixture.before),probe=fixture.blocked,release;
 const check=(k,v)=>{checks[k]=!!v;if(!v)throw Error(k);};
 page.on('pageerror',e=>errors.push(e.message));
 await page.addInitScript(id=>{sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};},sid);
 await page.route('**/*',async route=>{
  const r=route.request(),p=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=r.postData()?JSON.parse(r.postData()):undefined;requests.push({path:p,method:r.method(),body});
  if(p.endsWith('/execution/recovery-check'))return route.fulfill({json:probe});
  if(p==='/api/commands'){
   if(body.kind!=='recover_execution')return route.fulfill({status:409,json:{error:'unexpected command'}});
   await new Promise(resolve=>release=resolve);responses=clone(fixture.after);
   return route.fulfill({json:{command_id:body.command_id,session_id:sid,status:'accepted',operation_id:fixture.operation}});
  }
  if(responses[p])return route.fulfill({json:responses[p]});
  if(assets[p])return route.fulfill({path:assets[p],contentType:p.endsWith('.js')?'application/javascript':p.endsWith('.css')?'text/css':'text/html'});
  return route.fulfill({status:404,body:'fixture'});
 });
 await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9455/');
 const snapshot=page.locator('.execution-snapshot');await snapshot.locator(':scope > summary').click();
 const panel=page.getByRole('region',{name:'恢复前只读核对'});
 check('no_automatic_command',requests.every(r=>r.path!=='/api/commands'));
 check('recovery_requires_check',await panel.getByRole('button',{name:'按原配置恢复查询',exact:true}).count()===0);
 await panel.getByRole('button',{name:'核对恢复条件',exact:true}).click();await panel.getByText('还有条件未满足，保持待核对。',{exact:true}).waitFor();
 check('blocked_conditions_hide_recovery',await panel.getByRole('button',{name:'按原配置恢复查询',exact:true}).count()===0);
 probe=fixture.passed;await panel.getByRole('button',{name:'核对恢复条件',exact:true}).click();
 const recover=panel.getByRole('button',{name:'按原配置恢复查询',exact:true});await recover.waitFor();
 check('restart_and_side_effects_explained',(await panel.innerText()).includes('启动 hooks 可能再次执行')&&(await panel.innerText()).includes('自动化仍暂停'));
 check('input_blocked_before_recovery',await page.getByRole('button',{name:'发送消息',exact:false}).isDisabled());
 await panel.screenshot({path:'.dev/w0208-explicit-recovery/ready-1440.png'});
 await recover.click();
 for(let i=0;i<100&&!release;i++)await page.waitForTimeout(10);if(!release)throw Error('recovery command not sent');
 check('busy_prevents_double_click',await panel.getByRole('button',{name:'按原配置恢复查询',exact:true}).count()===0||await recover.isDisabled());
 const request=requests.find(r=>r.path==='/api/commands').body;
 check('command_exact_references',request.kind==='recover_execution'&&request.session_id===sid&&request.execution_recovery.base_snapshot_id===probe.base_snapshot_id&&request.execution_recovery.pending_change_id===probe.pending_change_id&&request.execution_recovery.operation_id===probe.native.operation);
 const acknowledgement=page.waitForResponse(r=>r.url().endsWith('/api/commands'));release();await acknowledgement;await page.evaluate(()=>window.dispatchEvent(new Event('online')));
 const archive=page.getByRole('region',{name:'未生效的配置尝试'});await archive.waitFor();await archive.locator(':scope > details > summary').click();
 check('candidate_archived',await archive.getByText('候选版本 2 · 已恢复原配置',{exact:true}).count()===1);
 check('failure_retained',await archive.getByRole('region',{name:'配置应用失败记录'}).count()===1);
 check('original_snapshot_retained',await snapshot.getByText(probe.base_snapshot_id,{exact:true}).count()===1);
 check('pending_recovery_removed',await panel.count()===0);
 check('automation_still_paused',await page.getByRole('button',{name:'恢复自动化',exact:true}).count()===1);
 await archive.getByText('完整候选快照与差异',{exact:true}).click();
 check('full_candidate_preserved',(await archive.locator('pre').innerText()).includes(probe.pending_change_id));
 await archive.getByText('完整候选快照与差异',{exact:true}).click();
 await archive.screenshot({path:'.dev/w0208-explicit-recovery/recovered-1440.png'});
 await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await archive.scrollIntoViewIfNeeded();
 check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));await archive.screenshot({path:'.dev/w0208-explicit-recovery/recovered-390.png'});
 responses=clone(fixture.failed);await page.setViewportSize({width:1440,height:1100});
 await page.reload();await page.locator('.execution-snapshot > summary').click();
 await page.getByText('按原配置恢复中 · 结果待核对，自动化仍暂停；不会重复启动。',{exact:true}).waitFor();
 check('separate_failure_records',await page.getByRole('region',{name:'配置应用失败记录',exact:true}).count()===1&&await page.getByRole('region',{name:'原配置恢复失败记录',exact:true}).count()===1);
 check('unknown_no_recovery_button',await page.getByRole('button',{name:'按原配置恢复查询',exact:true}).count()===0);
 check('unknown_input_blocked',await page.getByRole('button',{name:'发送消息',exact:false}).isDisabled());
 check('unknown_no_archive',await page.getByRole('region',{name:'未生效的配置尝试'}).count()===0);
 await page.getByRole('region',{name:'原配置恢复失败记录',exact:true}).scrollIntoViewIfNeeded();await page.screenshot({path:'.dev/w0208-explicit-recovery/unknown-1440.png'});
 check('one_explicit_command',requests.filter(r=>r.path==='/api/commands').length===1);
 check('no_page_errors',errors.length===0);
 return {checks,requests,errors,scope:'real App/native payloads with browser transport replay; no browser-launched native execution'};
}
