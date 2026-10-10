// Real Handler snapshots of a saved configuration + mixed capability candidate.
// Only browser command/SSE delivery is replayed; no native operations run here.
async page=>{
 const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[],clone=x=>JSON.parse(JSON.stringify(x));
 const sid=fixture.samples.initial.id,detail='/api/sessions/'+sid;
 let phase='staged',sequence=2000;
 const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
 page.setDefaultTimeout(6000);await page.unrouteAll({behavior:'ignoreErrors'});page.on('pageerror',e=>errors.push(e.message));
 await page.addInitScript(id=>{sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){this.listeners={};window.configMixedEvents=this;setTimeout(()=>this.onopen?.(),0);}addEventListener(k,f){this.listeners[k]=f;}close(){}};},sid);
 await page.route('**/*',async route=>{
  const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=r.postData()?JSON.parse(r.postData()):undefined;requests.push({path,method:r.method(),body});
  if(path==='/api/commands'){
   const next={prepare_execution:'prepared',apply_execution:'applied'}[body.kind];if(!next)return route.fulfill({status:409,body:'Unexpected command'});
   phase=next;sequence+=100;
   await route.fulfill({json:{command_id:body.command_id,session_id:sid,status:'accepted'}});
   await page.evaluate(()=>window.configMixedEvents.listeners.change?.());return;
  }
  if(fixture.responses[phase][path]){const data=clone(fixture.responses[phase][path]);data.sequence=sequence;return route.fulfill({json:data});}
  if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
  return route.fulfill({status:path.startsWith('/favicon')?204:404,body:''});
 });
 await page.setViewportSize({width:1440,height:1000});await page.goto('http://127.0.0.1:9463/');await page.locator('.execution-snapshot > summary').click();
 const snapshot=page.locator('.execution-snapshot'),panel=page.locator('.execution-change');
 check('saved_version_not_effective',(await panel.innerText()).includes('待应用版本 2 · 尚未生效'));
 check('setting_and_resources_in_one_candidate',(await panel.innerText()).includes('low → high')&&(await panel.innerText()).includes('adoption-bundle · CLI 插件')&&(await panel.innerText()).includes('loose-method · Skill'));
 check('original_snapshot_retained',(await snapshot.innerText()).includes(fixture.samples.initial.execution_snapshot.id));
 check('saved_config_choice',await panel.getByRole('checkbox',{name:'采纳当前配置来源版本',exact:true}).isChecked());
 check('no_implicit_hook_file_adoption',!await panel.getByRole('checkbox',{name:'采纳当前用户/项目 hook 文件版本',exact:true}).isChecked());
 check('input_still_blocked',await page.getByRole('button',{name:'发送消息',exact:true}).isDisabled());
 await panel.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).click();const commit=panel.getByRole('button',{name:'应用已保存配置',exact:true});await commit.waitFor();
 check('prepared_keeps_candidate',(await panel.innerText()).includes(fixture.samples.staged.pending_execution_change.snapshot.id));
 check('prepare_keeps_original',(await snapshot.innerText()).includes(fixture.samples.initial.execution_snapshot.id));
 await panel.scrollIntoViewIfNeeded();await page.screenshot({path:'.dev/w0209-config-mixed/prepared-1440.png',fullPage:true});
 await commit.click();await snapshot.getByText(fixture.samples.applied.execution_snapshot.id,{exact:true}).waitFor();
 check('combined_snapshot_promoted',!(await panel.innerText()).includes('待应用版本 2'));
 const plugins=page.getByRole('region',{name:'本次 CLI 插件选择'});
 const skills=page.getByRole('region',{name:'本次原生 Skill 选择'});
 check('selected_plugin_and_skill_shown',await plugins.locator('.snapshot-asset').count()===1&&(await skills.innerText()).includes('Owned independent Skill · 启用'));
 check('native_hook_and_package_verified',(await plugins.innerText()).includes('本连接 hook 配置已核对')&&(await skills.innerText()).includes('固定资源副本与整包策略已核对'));
 const source=page.locator('.configuration-source-snapshot');await source.first().locator(':scope > summary').click();
 check('new_effort_recorded',(await source.first().innerText()).includes('reasoning effort：high'));
 check('same_native_id',fixture.samples.applied.scope.native_id===fixture.samples.initial.scope.native_id);
 const commands=requests.filter(r=>r.path==='/api/commands');
 check('only_prepare_and_apply',commands.length===2&&commands[0].body.kind==='prepare_execution'&&commands[1].body.kind==='apply_execution');
 check('commands_bind_saved_candidate',commands.every(r=>r.body.execution_change.pending_change_id===fixture.samples.staged.pending_execution_change.id&&!r.body.execution_change.selections&&!r.body.execution_change.adopt_codex_configuration_sources));
 check('no_page_errors',errors.length===0);
 await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await plugins.scrollIntoViewIfNeeded();
 check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:'.dev/w0209-config-mixed/applied-390.png',fullPage:true});
 return {checks,passed:Object.values(checks).every(Boolean),requests,errors,scope:'actual configuration plus mixed capability Handler states; browser command/SSE replay'};
}
