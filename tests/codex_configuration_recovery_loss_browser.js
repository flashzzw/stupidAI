// Real Handler states after both a configuration application and its explicit
// recovery lose their native replies. No mutations are replayed in this test.
async page=>{
 const fixtures=__FIXTURES__,assets=__ASSETS__,checks={},requests=[],errors=[];
 let fixture=fixtures[0],phase='recovery_uncertain';
 const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
 page.setDefaultTimeout(6000);await page.unrouteAll({behavior:'ignoreErrors'});page.on('pageerror',e=>errors.push(e.message));
 await page.addInitScript(()=>{sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',new URL(location.href).searchParams.get('session')??'');window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};});
 await page.route('**/*',async route=>{
  const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];requests.push({path,method:r.method()});
  if(fixture.responses[phase][path])return route.fulfill({json:fixture.responses[phase][path]});
  if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
  return route.fulfill({status:path.startsWith('/favicon')?204:409,body:'No mutation in recovery-loss replay'});
 });
 await page.setViewportSize({width:1440,height:1000});
 for(const item of fixtures){
  fixture=item;
  for(const next of ['recovery_uncertain','recovery_reopened']){
   phase=next;await page.goto('http://127.0.0.1:9463/?session='+encodeURIComponent(fixture.sid));await page.locator('.execution-snapshot > summary').click();
   const label=fixture.mode+'/'+phase,panel=page.locator('.execution-change'),snapshot=page.locator('.execution-snapshot');
   await panel.getByText('按原配置恢复中 · 结果待核对，自动化仍暂停；不会重复启动。',{exact:true}).waitFor();
   check(label+'_unknown_explicit',(await panel.innerText()).includes('实际生效待核对'));
   check(label+'_original_and_candidate_retained',(await snapshot.innerText()).includes(fixture.original)&&(await panel.innerText()).includes(fixture.candidate));
   check(label+'_effort_delta_retained',(await panel.getByRole('region',{name:'待采纳配置来源'}).innerText()).includes('high → medium'));
   check(label+'_no_false_archive',await page.getByRole('region',{name:'未生效的配置尝试'}).count()===0);
   check(label+'_no_recovery_gate',await page.getByRole('region',{name:'恢复前只读核对'}).count()===0);
   check(label+'_no_apply_buttons',await panel.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).count()===0&&await panel.getByRole('button',{name:'应用已保存配置',exact:true}).count()===0);
   check(label+'_discard_disabled',await panel.getByRole('button',{name:'撤销待应用选择',exact:true}).isDisabled());
   check(label+'_input_disabled',await page.getByRole('button',{name:'发送消息',exact:true}).isDisabled());
   const choice=panel.getByRole('checkbox',{name:'采纳当前配置来源版本',exact:true});check(label+'_candidate_choice_locked',await choice.isChecked()&&await choice.isDisabled());
   const saved=page.locator('.configuration-source-snapshot').first();await saved.locator(':scope > summary').click();
   check(label+'_original_effort_recorded',(await saved.innerText()).includes('reasoning effort：high'));
  }
 }
 await page.locator('.execution-change').scrollIntoViewIfNeeded();await page.screenshot({path:'.dev/w0209-config-loss/unknown-1440.png',fullPage:true});
 await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await page.locator('.execution-change').scrollIntoViewIfNeeded();
 check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:'.dev/w0209-config-loss/unknown-390.png',fullPage:true});
 check('only_reads',requests.every(r=>r.method==='GET'));check('no_page_errors',errors.length===0);
 return {checks,passed:Object.values(checks).every(Boolean),requests,errors,scope:'four real double-loss states before/after persistent reopen; no browser recovery or other execution commands'};
}
