// Real native App response replay; compare live restoration with a lost bridge reply.
async page=>{
 const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
 let responses=fixture.live;
 const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
 await page.addInitScript(id=>{sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};},fixture.sid);
 page.on('pageerror',e=>errors.push(e.message));
 await page.route('**/*',async route=>{
  const r=route.request(),p=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];requests.push({path:p,method:r.method()});
  if(responses[p])return route.fulfill({json:responses[p]});
  if(assets[p])return route.fulfill({path:assets[p],contentType:p.endsWith('.js')?'application/javascript':p.endsWith('.css')?'text/css':'text/html'});
  return route.fulfill({status:404,body:'fixture'});
 });
 await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9455/');
 for(const state of ['live','offline']){
  if(state==='offline'){responses=fixture.offline;await page.reload();}
  const snapshot=page.locator('.execution-snapshot');await snapshot.locator(':scope > summary').click();
  const plugin=page.getByRole('region',{name:'本次 CLI 插件选择'}),skill=page.getByRole('region',{name:'本次原生 Skill 选择'});
  const native=responses['/api/sessions/'+fixture.sid].session,attempt=native.execution_change_attempts[0].change;
  check(state+'_original_snapshot',await snapshot.getByText(native.execution_snapshot.id,{exact:true}).count()===1);
  check(state+'_original_plugin_version',(await plugin.innerText()).includes('插件声明版本：1.0.0')&&!(await plugin.innerText()).includes('插件声明版本：2.0.0'));
  check(state+'_original_plugin_source',(await plugin.innerText()).includes(native.scope.plugin_selections[0].source_path));
  check(state+'_original_skill_source',(await skill.innerText()).includes(native.scope.skill_selections[0].source_path));
  check(state+'_original_plugin_state',(await plugin.innerText()).includes(state==='live'?'原生包与组件清单匹配':'原生包与组件待核对'));
  check(state+'_original_skill_state',(await skill.innerText()).includes(state==='live'?'原生命令与描述清单匹配':'原生选择待核对'));
  check(state+'_automation_paused',await page.getByRole('button',{name:'恢复自动化',exact:true}).count()===1);
  check(state+'_no_recovery_retry',await page.getByRole('button',{name:'按原配置恢复查询',exact:true}).count()===0);
  const archive=page.getByRole('region',{name:'未生效的配置尝试'});await archive.locator(':scope > details > summary').click();
  check(state+'_failed_candidate_separate',(await archive.innerText()).includes(attempt.snapshot.id));
  await archive.getByText('完整候选快照与差异',{exact:true}).click();const saved=JSON.parse(await archive.locator('pre').innerText());
  check(state+'_candidate_version_preserved',saved.snapshot.plugin_selections[0].version==='2.0.0'&&saved.snapshot.capability_refs.every(r=>r.asset.version==='v2'));
  check(state+'_separate_operations',saved.operation_id!==saved.recovery_operation_id&&saved.failure.operation===saved.operation_id);
  await archive.getByText('完整候选快照与差异',{exact:true}).click();
  await plugin.scrollIntoViewIfNeeded();await page.screenshot({path:'.dev/w0208-mixed-recovery/'+state+'-1440.png'});
  if(state==='offline'){
   check('offline_guard_not_registered',await snapshot.getByText('本地设置自动应用限制 · 已注册',{exact:true}).count()===0);
   await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await plugin.scrollIntoViewIfNeeded();
   check('offline_mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));await page.screenshot({path:'.dev/w0208-mixed-recovery/offline-390.png'});
  }
 }
 check('no_control_requests',requests.every(r=>r.method==='GET'));
 check('no_page_errors',errors.length===0);
 return {checks,requests,errors,scope:'real App response replay; live versus offline inventories and failed candidate archive; no native execution from browser'};
}
