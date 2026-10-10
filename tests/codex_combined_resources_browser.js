// Native snapshots/Handler exports, with controlled browser transport and ACKs.
async page => {
 page.setDefaultTimeout(6000);await page.unrouteAll({behavior:'ignoreErrors'});
 const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
 let phase='before';
 const check=(name,value)=>{checks[name]=!!value;if(!value)throw Error(name);};
 const snapshot=()=>phase==='before'?fixture.samples['A-call-v2']:phase==='recovered'?fixture.samples['combined-revoke-applied']:fixture.responses['/api/workspace'];
 page.on('pageerror',e=>errors.push(e.message));
 await page.addInitScript(id=>{sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{
  constructor(){this.listeners={};window.combinedEvents=this;setTimeout(()=>this.onopen?.(),0);}
  addEventListener(name,fn){this.listeners[name]=fn;}close(){}
 };},fixture.sessions.A);
 await page.route('**/*',async route=>{
  const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=r.postData()?JSON.parse(r.postData()):null;
  requests.push({path,method:r.method(),body});
  if(path==='/api/workspace')return route.fulfill({json:snapshot()});
  if(path==='/api/capabilities')return route.fulfill({json:fixture.responses[path]});
  if(path==='/api/capabilities/codex-hooks')return route.fulfill({json:fixture.samples['updated-inspection']});
  if(path.endsWith('/execution/preview'))return route.fulfill({json:fixture.samples['combined-revoke-preview']});
  if(path==='/api/commands')return route.fulfill({json:{session_id:fixture.sessions.A,status:body.kind==='stage_execution'?'pending':'accepted'}});
  if(path.startsWith('/api/sessions/')){
   if(phase==='final'&&fixture.responses[path])return route.fulfill({json:fixture.responses[path]});
   const s=snapshot(),id=path.slice('/api/sessions/'.length),session=s.sessions.find(v=>v.id===id);
   if(session)return route.fulfill({json:{sequence:s.sequence,session,operations:s.operations.filter(op=>op.session_id===id),events:[],total_messages:session.view.messages.length}});
  }
  if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
  return route.fulfill({status:404,json:{error:'unmapped fixture route'}});
 });
 await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9466/');
 const execution=page.locator('.execution-snapshot');if(await execution.getAttribute('open')===null)await execution.locator('summary').first().click();
 const skills=execution.getByRole('region',{name:'本次原生 Skill 选择'}),plugins=execution.getByRole('region',{name:'本次 CLI 插件选择'}),change=page.locator('.execution-change');
 check('separate_native_and_plugin_sources',await skills.count()===1&&await plugins.count()===1);
 check('current_directory_enabled',(await skills.innerText()).includes('native-directory · 启用'));
 check('current_hook_trusted',(await plugins.innerText()).includes('本版本 hook：已明确信任'));
 const picker=page.locator('.skill-input-picker');await picker.locator('summary').click();
 check('both_skill_inputs_available',await picker.getByRole('checkbox').count()===2);
 await change.locator('[data-skill-mode="directory"]').selectOption('disabled');
 const review=change.locator('.plugin-hook-review');await review.locator('summary').click();
 await review.getByRole('button',{name:'读取 hook 审核依据'}).click();await review.getByRole('status').waitFor();
 check('hook_review_defaults_untrusted',!await review.getByRole('checkbox').isChecked());
 await change.getByRole('button',{name:'预览改配差异',exact:true}).click();
 await change.getByText(/hook 信任：信任 → 未信任/).waitFor();
 check('combined_diff_shows_skill_and_hook',(await change.innerText()).includes('Native directory · Skill：启用 → 禁用')&&(await change.innerText()).includes('hook 信任：信任 → 未信任'));
 const preview=requests.filter(r=>r.path.endsWith('/execution/preview')).at(-1).body;
 check('preview_uses_original_snapshot',preview.base_snapshot_id===fixture.samples['combined-revoke-preview'].base_snapshot_id);
 check('preview_preserves_independent_versions',preview.selections.some(s=>s.asset_id==='directory'&&s.mode==='disabled'&&s.version==='directory-v1')&&preview.selections.some(s=>s.asset_id==='hooks'&&s.mode==='enabled'&&s.version==='asset-v2'&&s.hook_review.trust==='untrusted'));
 await change.getByRole('button',{name:'保存待应用选择'}).click();await page.getByText('待应用选择已保存，当前生效配置不变',{exact:true}).waitFor();
 check('save_does_not_change_current',(await skills.innerText()).includes('native-directory · 启用')&&(await plugins.innerText()).includes('已明确信任'));
 check('save_does_not_send_apply',!requests.some(r=>r.path==='/api/commands'&&r.body.kind==='apply_execution'));

 phase='recovered';await page.evaluate(()=>window.combinedEvents.listeners.change?.());
 await skills.getByText('native-directory · 禁用',{exact:true}).waitFor();
 check('recovered_directory_disabled',(await skills.innerText()).includes('原生来源与本连接 Skill 配置已核对'));
 check('recovered_hook_untrusted',(await plugins.innerText()).includes('本版本 hook：未信任'));
 check('recovered_candidate_cleared',!await change.getByText(/实际生效待核对/).count());
 check('recovered_picker_excludes_directory',await picker.getByRole('checkbox').count()===1);
 const lost=fixture.samples['application-receipt-loss'];
 const operation=page.locator('.operation').filter({hasText:lost.intent.id});
 check('recovered_original_operation_visible',await operation.count()===1&&(await operation.locator('summary').innerText()).includes('已完成'));
 await page.screenshot({path:'.dev/w0208-combined-resources/recovered-1440.png',fullPage:true});

 phase='final';await page.evaluate(()=>window.combinedEvents.listeners.change?.());
 await change.getByText(/实际生效待核对/).waitFor();
 check('unknown_keeps_current_selection',(await skills.innerText()).includes('native-directory · 启用')&&(await plugins.innerText()).includes('本版本 hook：已明确信任'));
 check('unknown_preserves_candidate_diff',(await change.innerText()).includes('Native directory · Skill：启用 → 禁用')&&(await change.innerText()).includes('hook 信任：信任 → 未信任'));
 check('unknown_explains_no_replay',(await change.innerText()).includes('回执丢失时保持暂停，不重复应用'));
 check('unknown_blocks_new_input',await page.getByRole('button',{name:'发送消息'}).isDisabled());
 check('unknown_blocks_automation_resume',await page.getByRole('button',{name:'恢复自动化',exact:true}).isDisabled());
 check('unknown_blocks_candidate_discard',await change.getByRole('button',{name:'撤销待应用选择'}).isDisabled());
 check('unknown_does_not_offer_reapply',await change.getByRole('button',{name:'应用已保存配置',exact:true}).count()===0);
 const hooks=page.locator('.hook-executions');if(await hooks.getAttribute('open')===null)await hooks.locator('summary').click();
 check('history_versions_preserved',(await hooks.innerText()).includes('资源 asset-v1')&&(await hooks.innerText()).includes('资源 asset-v2'));
 await change.scrollIntoViewIfNeeded();await page.screenshot({path:'.dev/w0208-combined-resources/uncertain-1440.png',fullPage:true});
 await page.getByRole('button',{name:'重新核对',exact:true}).click();
 check('reconcile_only_command',requests.filter(r=>r.path==='/api/commands').at(-1).body.kind==='reconcile');
 await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await change.scrollIntoViewIfNeeded();
 check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
 await page.screenshot({path:'.dev/w0208-combined-resources/uncertain-390.png',fullPage:true});
 check('no_configuration_or_model_replay',requests.filter(r=>r.path==='/api/commands').every(r=>['stage_execution','reconcile'].includes(r.body.kind)));
 check('no_page_errors',errors.length===0);
 return {checks,requests,errors,scope:'built UI; actual native snapshots, receipt-loss outcomes and Handler exports; controlled HTTP/SSE/command ACKs; no browser native execution'};
}
