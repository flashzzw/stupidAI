// Real native Application snapshots, with controlled HTTP/SSE and command receipt.
async page=>{
 page.setDefaultTimeout(6000);
 const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[],clone=x=>JSON.parse(JSON.stringify(x));
 let state=fixture.before,mode='blocked',release;
 const sid=fixture.sid,path='/api/sessions/'+sid;
 const check=(k,v)=>{checks[k]=!!v;if(!v)throw Error(k);};
 await page.addInitScript(id=>{sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){this.listeners={};window.recoveryEvents=this;setTimeout(()=>this.onopen?.(),0);}addEventListener(k,f){this.listeners[k]=f;}close(){}};},sid);
 page.on('pageerror',e=>errors.push(e.message));
 await page.route('**/*',async route=>{
  const r=route.request(),p=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=r.postData()?JSON.parse(r.postData()):null;requests.push({path:p,method:r.method(),body});
  if(p===path+'/execution/recovery-check'){
   const out=clone(fixture.report);
   if(mode==='blocked'){out.satisfied=false;out.native.checks.find(c=>c.kind==='thread_unloaded').status='blocked';}
   if(mode==='wrong-backend')out.native.source='claude.configuration_recovery';
   if(mode==='missing-list')out.native.checks=out.native.checks.filter(c=>c.kind!=='thread_unloaded');
   return route.fulfill({json:out});
  }
  if(p==='/api/commands'){
   if(body.kind!=='recover_execution')return route.fulfill({status:409,json:{error:'unexpected command'}});
   await new Promise(resolve=>release=resolve);state=fixture.after;
   await route.fulfill({json:{command_id:body.command_id,session_id:sid,status:'accepted'}});
   await page.evaluate(()=>window.recoveryEvents.listeners.change?.());return;
  }
  if(p==='/api/workspace')return route.fulfill({json:state});
  if(p.startsWith('/api/sessions/')){
   const id=p.slice('/api/sessions/'.length),s=state.sessions.find(s=>s.id===id);
   if(s)return route.fulfill({json:{sequence:state.sequence,session:s,operations:state.operations.filter(o=>o.session_id===id),events:[],total_messages:s.view.messages.length}});
  }
  if(assets[p])return route.fulfill({path:assets[p],contentType:p.endsWith('.js')?'application/javascript':p.endsWith('.css')?'text/css':'text/html'});
  return route.fulfill({status:404,json:{error:'unmapped fixture route'}});
 });
 await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9466/');
 const snapshot=page.locator('.execution-snapshot');await snapshot.locator(':scope > summary').click();
 const panel=page.getByRole('region',{name:'恢复前只读核对'}),probe=panel.getByRole('button',{name:'核对恢复条件',exact:true});
 check('no_automatic_probe',!requests.some(r=>r.path.endsWith('/recovery-check')));
 check('no_unchecked_restore',await panel.getByRole('button',{name:'按原配置恢复原线程',exact:true}).count()===0);
 check('unknown_input_disabled',await page.getByRole('button',{name:'发送消息',exact:false}).isDisabled());
 await probe.click();await panel.getByText('还有条件未满足，保持待核对。',{exact:true}).waitFor();
 check('loaded_thread_prevents_restore',await panel.getByRole('button',{name:'按原配置恢复原线程',exact:true}).count()===0);
 check('native_unloaded_condition_visible',(await panel.innerText()).includes('原线程与加载清单均确认未加载：未满足'));
 for(const bad of ['wrong-backend','missing-list']){mode=bad;await probe.click();await panel.getByRole('alert').waitFor();check(bad+'_rejected',await panel.getByRole('button',{name:'按原配置恢复原线程',exact:true}).count()===0);}
 mode='passed';await probe.click();const restore=panel.getByRole('button',{name:'按原配置恢复原线程',exact:true});await restore.waitFor();
 check('original_configuration_condition',(await panel.innerText()).includes('原配置来源与约束可核对：已核对'));
 check('boundary_explained',(await panel.innerText()).includes('不关闭共享后台')&&(await panel.innerText()).includes('历史未变'));
 const request=requests.find(r=>r.path.endsWith('/recovery-check')).body;
 check('check_exact_saved_references',request.base_snapshot_id===fixture.report.base_snapshot_id&&request.pending_change_id===fixture.report.pending_change_id&&request.operation_id===fixture.report.native.operation);
 await panel.screenshot({path:'.dev/w0208-codex-recovery/ready-1440.png'});
 await restore.click();for(let i=0;i<100&&!release;i++)await page.waitForTimeout(10);if(!release)throw Error('recovery command did not start');
 check('busy_prevents_repeated_restore',await panel.getByRole('button',{name:'按原配置恢复原线程',exact:true}).count()===0||await restore.isDisabled());
 const command=requests.find(r=>r.path==='/api/commands').body;
 check('restore_exact_binding',command.kind==='recover_execution'&&command.session_id===sid&&JSON.stringify(command.execution_recovery)===JSON.stringify(request));
 release();const archive=page.getByRole('region',{name:'未生效的配置尝试'});await archive.waitFor();await archive.locator(':scope > details > summary').click();
 check('original_snapshot_retained',await snapshot.getByText(fixture.report.base_snapshot_id,{exact:true}).count()===1);
 check('candidate_retained',(await archive.innerText()).includes(fixture.candidate));
 check('unverified_attempt_reason_visible',(await archive.innerText()).includes('原生改配未取得完整确认'));
 check('automation_remains_paused',await page.getByRole('button',{name:'恢复自动化',exact:true}).count()===1);
 check('restore_panel_removed',await panel.count()===0);
 const skills=page.getByRole('region',{name:'本次原生 Skill 选择'}),plugins=page.getByRole('region',{name:'本次 CLI 插件选择'});
 check('original_skill_enabled',(await skills.innerText()).includes('native-directory · 启用'));
 check('original_hook_trusted',(await plugins.innerText()).includes('本版本 hook：已明确信任'));
 await archive.scrollIntoViewIfNeeded();await page.screenshot({path:'.dev/w0208-codex-recovery/recovered-1440.png'});
 state=fixture.unknown;await page.reload();await page.locator('.execution-snapshot > summary').click();
 await page.getByText('按原配置恢复中 · 结果待核对，自动化仍暂停；不会重复启动。',{exact:true}).waitFor();
 check('lost_recovery_keeps_candidate',await page.getByRole('region',{name:'未生效的配置尝试'}).count()===0&&await page.locator('.execution-change').getByText(/候选快照/).count()===1);
 check('candidate_result_stays_unknown',(await page.locator('.execution-change').innerText()).includes('实际生效待核对'));
 check('lost_recovery_no_retry',await page.getByRole('button',{name:'按原配置恢复原线程',exact:true}).count()===0);
 check('lost_recovery_input_blocked',await page.getByRole('button',{name:'发送消息',exact:false}).isDisabled());
 await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await page.getByText('按原配置恢复中 · 结果待核对，自动化仍暂停；不会重复启动。',{exact:true}).scrollIntoViewIfNeeded();
 check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));await page.screenshot({path:'.dev/w0208-codex-recovery/unknown-390.png'});
 check('single_explicit_command',requests.filter(r=>r.path==='/api/commands').length===1);check('no_page_errors',errors.length===0);
 return {checks,requests,errors,scope:'native App snapshots, HTTP/SSE and boundary reports replayed; no native execution from browser'};
}
