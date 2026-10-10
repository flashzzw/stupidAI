// Native Application snapshots/Handler exports; transport and command acknowledgements are controlled.
async page => {
  page.setDefaultTimeout(5000);await page.unrouteAll({behavior:'ignoreErrors'});
  const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
  let phase='initial',mode='normal',release,delivered;
  const check=(name,value)=>{checks[name]=!!value;if(!value)throw Error(name);};
  const clone=value=>JSON.parse(JSON.stringify(value));
  const inspectionPath='/api/capabilities/codex-hooks';
  const snapshot=()=>phase==='final'?fixture.responses['/api/workspace']:fixture.samples.initial;
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{
    constructor(){this.listeners={};window.hookSelectionEvents=this;setTimeout(()=>this.onopen?.(),0);}
    addEventListener(name,fn){this.listeners[name]=fn;}close(){}
  };},fixture.sessions.A);
  await page.route('**/*',async route=>{
    const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=r.postData()?JSON.parse(r.postData()):null;
    requests.push({path,method:r.method(),body});
    if(path==='/api/workspace')return route.fulfill({json:snapshot()});
    if(path==='/api/capabilities')return route.fulfill({json:phase==='final'?fixture.responses[path]:fixture.samples['initial-capabilities']});
    if(path===inspectionPath){
      if(mode==='failure')return route.fulfill({status:409,json:{error:'审核来源已变化，请重新读取。'}});
      const value=clone(fixture.samples[phase==='final'?'updated-inspection':'initial-inspection']);
      if(mode==='mismatch')value.request.instance_id='different-instance';
      if(mode==='delayed')await new Promise(resolve=>{release=resolve;});
      await route.fulfill({json:value});delivered?.();delivered=undefined;return;
    }
    if(path.endsWith('/execution/preview'))return route.fulfill({json:fixture.samples['A-revoke-preview']});
    if(path==='/api/commands')return route.fulfill({json:{session_id:fixture.sessions.A,status:body.kind==='stage_execution'?'pending':'accepted'}});
    if(path.startsWith('/api/sessions/')){
      if(phase==='final'&&fixture.responses[path])return route.fulfill({json:fixture.responses[path]});
      const id=path.slice('/api/sessions/'.length),s=snapshot();
      const session=s.sessions.find(v=>v.id===id);
      if(session)return route.fulfill({json:{sequence:s.sequence,session,operations:s.operations.filter(op=>op.session_id===id),events:[],total_messages:session.view.messages.length}});
    }
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,json:{error:'unmapped fixture route'}});
  });
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9464/');
  await page.getByRole('button',{name:'＋ 新建',exact:true}).click();
  let dialog=page.getByRole('dialog');
  await dialog.locator('[name="capability_selection:hooks"]').selectOption('enabled');
  let panel=dialog.locator('.plugin-hook-review');await panel.locator('summary').click();
  check('no_automatic_review',!requests.some(r=>r.path===inspectionPath));
  await panel.getByRole('button',{name:'读取 hook 审核依据'}).click();await panel.getByRole('status').waitFor();
  check('untrusted_default',!await panel.getByRole('checkbox').isChecked());
  let hidden=dialog.locator('[name="capability_hook_review:hooks"]');
  check('server_receipt_bound',JSON.parse(await hidden.inputValue()).inspection_id===fixture.samples['initial-inspection'].inspection_id);
  check('review_does_not_auto_trust',JSON.parse(await hidden.inputValue()).trust==='untrusted');
  let text=await panel.innerText();
  check('review_exposes_version_source_event',text.includes('asset-v1')&&text.includes('完整内容标识')&&text.includes('userPromptSubmit')&&text.includes('hooks/hooks.json'));
  check('review_does_not_expose_command',!text.includes('os.execl')&&!text.includes('python3')&&!text.includes('PRIVATE_'));
  await panel.getByRole('checkbox').check();
  check('trust_is_explicit',JSON.parse(await hidden.inputValue()).trust==='trusted');
  await panel.scrollIntoViewIfNeeded();await page.screenshot({path:'.dev/w0208-hook-selection/review-1440.png',fullPage:true});
  await dialog.getByRole('button',{name:'创建会话',exact:true}).click();await dialog.waitFor({state:'hidden'});
  const create=requests.find(r=>r.path==='/api/commands'&&r.body.kind==='create');
  check('create_sends_trust_receipt',create.body.capability_selections[0].hook_review.inspection_id===fixture.samples['initial-inspection'].inspection_id&&create.body.capability_selections[0].hook_review.trust==='trusted');
  check('create_cannot_supply_native_trust_key',Object.keys(create.body.capability_selections[0].hook_review).sort().join(',')==='inspection_id,trust');
  const execution=page.locator('.execution-snapshot');if(await execution.getAttribute('open')===null)await execution.locator('summary').first().click();
  const change=page.locator('.execution-change');panel=change.locator('.plugin-hook-review');await panel.locator('summary').click();
  await panel.getByRole('button',{name:'读取 hook 审核依据'}).click();await panel.getByRole('status').waitFor();
  await change.getByRole('button',{name:'预览改配差异',exact:true}).click();
  await change.getByText(/hook 信任：信任 → 未信任/).waitFor();
  check('revocation_diff_is_visible',(await change.innerText()).includes('hook 信任：信任 → 未信任'));
  const preview=requests.filter(r=>r.path.endsWith('/execution/preview')).at(-1);
  check('preview_uses_original_snapshot',preview.body.base_snapshot_id===fixture.samples.initial.sessions.find(s=>s.id===fixture.sessions.A).execution_snapshot.id);
  check('preview_sends_untrusted_review',preview.body.selections.find(s=>s.asset_id==='hooks').hook_review.trust==='untrusted');
  check('reading_does_not_write_configuration',requests.filter(r=>r.path==='/api/commands').length===1);
  await change.getByRole('button',{name:'保存待应用选择'}).click();
  await page.getByText('待应用选择已保存，当前生效配置不变',{exact:true}).waitFor();
  const stage=requests.find(r=>r.path==='/api/commands'&&r.body.kind==='stage_execution');
  check('saved_candidate_keeps_explicit_review',stage.body.execution_change.selections.find(s=>s.asset_id==='hooks').hook_review.trust==='untrusted');
  check('stage_does_not_send_apply',!requests.some(r=>r.path==='/api/commands'&&r.body.kind==='apply_execution'));

  const originalSource=fixture.samples.initial.capability_assets[0].source;
  fixture.samples.initial.capability_assets[0].source=originalSource+'-changed';
  await page.evaluate(()=>window.hookSelectionEvents.listeners.change?.());
  await panel.getByRole('status').waitFor({state:'hidden'});
  check('source_change_discards_preview',await change.getByRole('button',{name:'保存待应用选择'}).count()===0);
  await change.getByRole('button',{name:'预览改配差异',exact:true}).click();
  await change.getByText(/hook 信任：信任 → 未信任/).waitFor();
  const changedSourcePreview=requests.filter(r=>r.path.endsWith('/execution/preview')).at(-1);
  check('source_change_discards_parent_review',!changedSourcePreview.body.selections.some(s=>s.hook_review));
  fixture.samples.initial.capability_assets[0].source=originalSource;
  await page.evaluate(()=>window.hookSelectionEvents.listeners.change?.());

  await page.getByRole('button',{name:'＋ 新建',exact:true}).click();dialog=page.getByRole('dialog');
  await dialog.locator('[name="capability_selection:hooks"]').selectOption('enabled');
  panel=dialog.locator('.plugin-hook-review');await panel.locator('summary').click();
  mode='mismatch';await panel.getByRole('button',{name:'读取 hook 审核依据'}).click();await panel.getByRole('alert').waitFor();
  check('wrong_instance_receipt_rejected',await dialog.locator('[name="capability_hook_review:hooks"]').count()===0);
  mode='normal';await panel.getByRole('button',{name:'读取 hook 审核依据'}).click();await panel.getByRole('status').waitFor();
  mode='failure';await panel.getByRole('button',{name:'读取 hook 审核依据'}).click();await panel.getByRole('alert').waitFor();
  check('failed_refresh_clears_previous_review',await dialog.locator('[name="capability_hook_review:hooks"]').count()===0);
  mode='delayed';const settled=new Promise(resolve=>{delivered=resolve;});
  await panel.getByRole('button',{name:'读取 hook 审核依据'}).click();await panel.getByRole('button',{name:'正在核对…',exact:true}).waitFor();
  check('duplicate_review_disabled',await panel.getByRole('button',{name:'正在核对…',exact:true}).isDisabled());
  await dialog.getByRole('button',{name:'关闭新建会话'}).click();
  while(!release)await new Promise(resolve=>setTimeout(resolve,10));release();await settled;
  await page.getByRole('button',{name:'＋ 新建',exact:true}).click();dialog=page.getByRole('dialog');
  await dialog.locator('[name="capability_selection:hooks"]').selectOption('enabled');
  check('late_closed_form_receipt_ignored',await dialog.locator('[name="capability_hook_review:hooks"]').count()===0);
  panel=dialog.locator('.plugin-hook-review');await panel.locator('summary').click();mode='normal';
  await panel.getByRole('button',{name:'读取 hook 审核依据'}).click();await panel.getByRole('status').waitFor();await panel.getByRole('checkbox').check();
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  check('offline_discards_form_review',await dialog.locator('[name="capability_hook_review:hooks"]').count()===0);
  check('offline_disables_review',await dialog.locator('.plugin-hook-review button').isDisabled());
  await page.evaluate(()=>window.dispatchEvent(new Event('online')));
  await page.waitForFunction(()=>!document.querySelector('.dialog .plugin-hook-review button')?.disabled);
  check('reconnect_does_not_reuse_trust',await dialog.locator('[name="capability_hook_review:hooks"]').count()===0);
  await dialog.getByRole('button',{name:'关闭新建会话'}).click();

  phase='final';await page.evaluate(()=>window.hookSelectionEvents.listeners.change?.());
  await page.getByText('原操作绑定：hooks · 资源 asset-v2').first().waitFor({state:'attached'});
  if(await execution.getAttribute('open')===null)await execution.locator('summary').first().click();
  const history=page.locator('.hook-executions');if(await history.getAttribute('open')===null)await history.locator('summary').click();
  text=await history.innerText();
  check('historical_asset_version_preserved',text.includes('asset-v1')&&text.includes('asset-v2'));
  check('actual_timeout_not_success',text.includes('原生报告：hook 超时')&&!text.includes('成功结束'));
  check('snapshot_provenance_visible',text.includes('原操作绑定')&&text.includes('完整内容标识')&&text.includes('快照'));
  check('connection_scope_label_visible',(await execution.innerText()).includes('本连接 hook 配置已核对'));
  await page.screenshot({path:'.dev/w0208-hook-selection/execution-1440.png',fullPage:true});
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await history.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.screenshot({path:'.dev/w0208-hook-selection/execution-390.png',fullPage:true});
  check('only_expected_commands',requests.filter(r=>r.path==='/api/commands').every(r=>['create','stage_execution'].includes(r.body.kind)));
  check('no_direct_config_writes',!requests.some(r=>/config\/.*write|trust/.test(r.path)));
  check('no_page_errors',errors.length===0);
  return {checks,requests,errors,scope:'built UI; native snapshots, inspection receipts, preview and final Handler exports; controlled HTTP/SSE/command acknowledgements and synthetic failure/delay; no browser native execution'};
}
