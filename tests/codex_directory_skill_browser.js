// Real native snapshots and Handler exports; controlled browser transport/ACKs.
async page => {
  page.setDefaultTimeout(6000);await page.unrouteAll({behavior:'ignoreErrors'});
  const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
  let phase='initial';
  const check=(name,value)=>{checks[name]=!!value;if(!value)throw Error(name);};
  const snapshot=()=>phase==='initial'?fixture.samples.initial:fixture.responses['/api/workspace'];
  page.on('pageerror',error=>errors.push(error.message));
  await page.addInitScript(id=>{sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{
    constructor(){this.listeners={};window.directorySkillEvents=this;setTimeout(()=>this.onopen?.(),0);}
    addEventListener(name,fn){this.listeners[name]=fn;}close(){}
  };},fixture.sessions.A);
  await page.route('**/*',async route=>{
    const request=route.request(),path=request.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=request.postData()?JSON.parse(request.postData()):null;
    requests.push({path,method:request.method(),body});
    if(path==='/api/workspace')return route.fulfill({json:snapshot()});
    if(path==='/api/capabilities')return route.fulfill({json:phase==='initial'?fixture.samples['initial-capabilities']:fixture.responses[path]});
    if(path.endsWith('/skills/context'))return route.fulfill({json:fixture.samples['final-history']});
    if(path.endsWith('/execution/preview'))return route.fulfill({json:fixture.samples['A-disable-preview']});
    if(path==='/api/commands')return route.fulfill({json:{session_id:fixture.sessions.A,status:body.kind==='stage_execution'?'pending':'accepted'}});
    if(path.startsWith('/api/sessions/')){
      if(phase==='final'&&fixture.responses[path])return route.fulfill({json:fixture.responses[path]});
      const state=snapshot(),id=path.slice('/api/sessions/'.length),session=state.sessions.find(s=>s.id===id);
      if(session)return route.fulfill({json:{sequence:state.sequence,session,operations:state.operations.filter(op=>op.session_id===id),events:[],total_messages:session.view.messages.length}});
    }
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,json:{error:'unmapped browser fixture path'}});
  });
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9465/');
  await page.getByRole('button',{name:'＋ 新建',exact:true}).click();
  let dialog=page.getByRole('dialog');
  await dialog.locator('[name="capability_selection:user"]').selectOption('enabled');
  await dialog.locator('[name="capability_selection:project"]').selectOption('disabled');
  check('create_explains_native_resource', (await dialog.innerText()).includes('按登记的原生入口选择用户/项目 Skill'));
  check('create_no_hook_review',await dialog.locator('.plugin-hook-review').count()===0);
  await page.screenshot({path:'.dev/w0207-native-skill/create-1440.png',fullPage:true});
  await dialog.getByRole('button',{name:'创建会话',exact:true}).click();await dialog.waitFor({state:'hidden'});
  const create=requests.find(r=>r.path==='/api/commands'&&r.body.kind==='create');
  check('create_selects_two_assets',create.body.capability_selections.length===2&&create.body.capability_selections.some(s=>s.asset_id==='user'&&s.mode==='enabled')&&create.body.capability_selections.some(s=>s.asset_id==='project'&&s.mode==='disabled'));
  check('create_cannot_override_native_path',create.body.capability_selections.every(s=>Object.keys(s).sort().join(',')==='asset_id,mode'));
  const execution=page.locator('.execution-snapshot');if(await execution.getAttribute('open')===null)await execution.locator('summary').first().click();
  const native=execution.getByRole('region',{name:'本次原生 Skill 选择'});
  await native.getByText('原生来源与本连接 Skill 配置已核对',{exact:true}).first().waitFor();
  let text=await native.innerText();
  check('native_paths_visible',text.includes(fixture.samples.initial.capability_assets.find(a=>a.id==='user').codex_skill));
  check('original_resource_not_pack',text.includes('使用原生目录资源，未复制为插件')&&!text.includes('为本会话加载独立 Skill 包'));
  check('source_categories_visible',text.includes('原生来源类别：用户')&&text.includes('原生来源类别：项目'));
  check('configuration_not_description_claim',!text.includes('原生命令与描述清单匹配')&&!text.includes('命令清单：'));
  const picker=page.locator('.skill-input-picker');await picker.locator('summary').click();
  check('only_enabled_skill_available',await picker.getByRole('checkbox').count()===1&&(await picker.innerText()).includes('native-user · 原生目录'));
  await picker.getByRole('checkbox').check();await page.locator('#message').fill('OWNED browser input');
  await page.getByRole('button',{name:'发送消息'}).click();
  await page.waitForFunction(()=>document.querySelector('#message')?.value==='');
  const send=requests.find(r=>r.path==='/api/commands'&&r.body.kind==='send');
  check('explicit_input_uses_snapshot',send.body.skill_input.snapshot_id===fixture.samples.initial.sessions.find(s=>s.id===fixture.sessions.A).execution_snapshot.id);
  check('explicit_input_uses_asset_and_name',send.body.skill_input.items.length===1&&send.body.skill_input.items[0].asset_id==='user'&&send.body.skill_input.items[0].name==='native-user'&&Object.keys(send.body.skill_input.items[0]).sort().join(',')==='asset_id,name');
  check('accepted_input_clears_selection',!await picker.getByRole('checkbox').isChecked());
  const change=page.locator('.execution-change');
  await change.locator('[data-skill-mode="user"]').selectOption('disabled');
  await change.getByRole('button',{name:'预览改配差异',exact:true}).click();
  await change.getByText(/Native user.*启用 → 禁用/).waitFor();
  const preview=requests.filter(r=>r.path.endsWith('/execution/preview')).at(-1);
  check('change_keeps_resource_versions',preview.body.selections.every(s=>s.version==='v1'));
  check('native_skill_diff_visible',(await change.innerText()).includes('Native user')&&(await change.innerText()).includes('启用 → 禁用'));
  await change.getByRole('button',{name:'保存待应用选择'}).click();
  await page.getByText('待应用选择已保存，当前生效配置不变',{exact:true}).waitFor();
  check('save_does_not_apply',!requests.some(r=>r.path==='/api/commands'&&r.body.kind==='apply_execution'));
  check('save_keeps_current_snapshot',(await native.innerText()).includes('native-user · 启用'));

  phase='final';await page.evaluate(()=>window.directorySkillEvents.listeners.change?.());
  await picker.getByText('资产版本：v2',{exact:true}).waitFor({state:'attached'});
  if(await execution.getAttribute('open')===null)await execution.locator('summary').first().click();
  const context=page.locator('.skill-context');await context.locator('summary').first().click();
  await context.getByRole('button',{name:'读取 Skill 上下文'}).click();
  await context.getByRole('status').waitFor();
  check('current_version_body_visible',(await context.innerText()).includes('请求资产版本 v2')&&(await context.innerText()).includes('请求版本正文已载入'));
  const oldFrame=context.locator('.skill-context-frame').filter({hasText:'请求资产版本 v1'}).first();await oldFrame.locator('summary').first().click();
  check('historical_version_body_preserved',(await oldFrame.innerText()).includes('请求资产版本 v1')&&(await oldFrame.innerText()).includes('请求版本正文已载入'));
  check('method_gate_not_claimed',(await context.innerText()).includes('不证明模型已收到、方法已使用或 Gate 通过'));
  await page.screenshot({path:'.dev/w0207-native-skill/execution-1440.png',fullPage:true});
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));
  await native.getByText('连接已断开，选择待核对',{exact:true}).first().waitFor();
  check('offline_withdraws_current_verification',(await native.innerText()).includes('连接已断开，选择待核对'));
  check('offline_blocks_input',await page.getByRole('button',{name:'发送消息'}).isDisabled());
  await page.evaluate(()=>window.dispatchEvent(new Event('online')));
  await native.getByText('原生来源与本连接 Skill 配置已核对',{exact:true}).first().waitFor();
  await page.locator('.session-row').filter({hasText:'Directory C'}).click();
  await page.getByRole('heading',{name:'Directory C',exact:true}).waitFor();
  const stale=page.locator('.execution-snapshot');if(await stale.getAttribute('open')===null)await stale.locator('summary').first().click();
  await stale.getByText(/原生 Skill 完整资源内容或权限已变化/).first().waitFor();
  check('stale_sibling_not_upgraded',(await stale.innerText()).includes('Native user · v1'));
  check('stale_sibling_blocks_input',await page.getByRole('button',{name:'发送消息'}).isDisabled());
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();
  await stale.getByRole('region',{name:'本次原生 Skill 选择'}).scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.screenshot({path:'.dev/w0207-native-skill/drift-390.png',fullPage:true});
  await page.setViewportSize({width:1440,height:1100});await page.getByRole('button',{name:'能力库',exact:true}).click();
  const card=page.locator('.capability-card').filter({has:page.getByRole('heading',{name:'Native user',exact:true})});
  await card.getByRole('button',{name:'更新登记版本',exact:true}).click();dialog=page.getByRole('dialog');
  check('registration_retains_exact_mapping',await dialog.locator('[name="codex_skill"]').inputValue()===fixture.samples.initial.capability_assets.find(a=>a.id==='user').codex_skill);
  check('registration_explains_external_mapping',(await dialog.innerText()).includes('原生 Skill 映射仅适用于 Codex 外部资源'));
  await dialog.getByRole('button',{name:'关闭能力管理',exact:true}).click();
  check('no_implicit_source_or_native_writes',requests.filter(r=>r.path==='/api/commands').every(r=>['create','send','stage_execution'].includes(r.body.kind)));
  check('no_page_errors',errors.length===0);
  return {checks,requests,errors,scope:'built UI; actual native Application snapshots, context records and Handler exports; controlled HTTP/SSE/command acknowledgements; no browser native execution'};
}
