// Built UI with native Handler data; HTTP loss, snapshot drift and evidence
// mismatch are controlled browser cases, not additional native execution.
async page => {
  page.setDefaultTimeout(5000);await page.unrouteAll({behavior:'ignoreErrors'});
  const fixture=__FIXTURE__,assets=__ASSETS__,sid=fixture.session,responses=JSON.parse(JSON.stringify(fixture.responses));
  const pack0=responses['/api/sessions/'+sid].session.execution_snapshot.plugin_selections[0];
  const screenshotRoot='.dev/w0207-shared-skill-'+pack0.skill_asset.kind;
  const sessionPath='/api/sessions/'+sid,contextPath=sessionPath+'/skills/context';
  const originalSnapshot=responses[sessionPath].session.execution_snapshot.id;
  const checks={},requests=[],errors=[];let lose=true,mismatch=false;
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};},sid);
  await page.route('**/*',async route=>{
    const request=route.request(),path=request.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=request.postData()?JSON.parse(request.postData()):null;
    requests.push({path,method:request.method(),body});
    if(path==='/api/commands'){
      if(body?.kind==='create')return route.fulfill({status:202,json:{command_id:body.command_id,session_id:sid,status:'accepted'}});
      check('only_message_input_submitted',body?.kind==='send');
      if(lose){lose=false;return route.abort('failed');}
      return route.fulfill({status:202,json:{command_id:body.command_id,session_id:sid,status:'accepted'}});
    }
    if(path.endsWith('/execution/preview'))return route.fulfill({status:409,json:{error:'controlled preview payload boundary'}});
    if(path===contextPath){
      check('context_read_is_empty_post',request.method()==='POST'&&JSON.stringify(body)==='{}');
      const data=JSON.parse(JSON.stringify(responses[path]));if(mismatch)data.frames.at(-1).input_evidence.items[0].status='content_mismatch';
      return route.fulfill({json:data});
    }
    if(responses[path])return route.fulfill({json:responses[path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'unmapped fixture'});
  });
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9471/');
  const picker=page.locator('.skill-input-picker');await picker.locator('summary').click();
  const first=picker.getByRole('checkbox').first();
  check('one_shared_method_choice',await picker.getByRole('checkbox').count()===1);
  check('skills_opt_in',!await first.isChecked());
  check('saved_asset_version_visible',(await picker.innerText()).includes('资产版本：asset-v2'));
  await first.check();await page.locator('#message').fill('Use selected method.');
  const send=page.getByRole('button',{name:'发送消息'});await send.click();await page.getByRole('alert').waitFor();
  check('lost_http_receipt_keeps_draft',await page.locator('#message').inputValue()==='Use selected method.');
  check('lost_http_receipt_keeps_skill_choices',await first.isChecked());
  await send.click();await page.waitForFunction(()=>document.querySelector('#message').value==='');
  const commands=requests.filter(r=>r.path==='/api/commands');
  check('retry_uses_same_command_id',commands.length===2&&commands[0].body.command_id===commands[1].body.command_id);
  const command=commands[1].body;
  check('request_pins_visible_snapshot',command.skill_input?.snapshot_id===originalSnapshot);
  check('original_asset_and_native_method_serialized',command.skill_input?.items.length===1&&command.skill_input.items[0].asset_id==='mixed'&&command.skill_input.items[0].name===pack0.components[0].native_name);
  check('browser_cannot_choose_native_path',command.skill_input.items.every(item=>Object.keys(item).sort().join(',')==='asset_id,name'));
  check('accepted_request_clears_choices',!await first.isChecked());
  await page.locator('.execution-snapshot>summary').click();const context=page.locator('.skill-context');await context.locator('summary').first().click();
  const selected=page.getByRole('region',{name:'本次原生 Skill 选择'});
  check('skill_asset_kind_and_version_visible',(await selected.innerText()).includes('Skill 资产 · asset-v2'));
  check('resource_policy_coverage_visible',(await selected.innerText()).includes('固定资源副本与整包策略已核对'));
  check('not_presented_as_cli_plugin',await page.getByRole('region',{name:'本次 CLI 插件选择'}).count()===0);
  await selected.getByText('资源与交付来源',{exact:true}).click();
  check('original_and_delivery_sources_visible',(await selected.innerText()).includes(pack0.source_path)&&(await selected.innerText()).includes(pack0.cache_path));
  check('resource_kind_visible',(await selected.innerText()).includes(pack0.skill_asset.kind==='file'?'单文件入口':'目录及相对资料'));
  const change=page.getByRole('region',{name:'原会话能力改配'});
  check('skill_reconfiguration_controls',await change.locator('[data-skill-mode="mixed"]').count()===1);
  check('skill_can_restore_inheritance',await change.locator('[data-skill-mode="mixed"] option[value="inherit"]').count()===1);
  await change.locator('[data-skill-mode="mixed"]').selectOption('disabled');
  await change.getByRole('button',{name:'预览改配差异',exact:true}).click();
  await change.getByText('controlled preview payload boundary',{exact:true}).waitFor();
  const preview=requests.find(r=>r.path.endsWith('/execution/preview'));
  check('skill_change_keeps_snapshot_and_version',preview.body.base_snapshot_id===originalSnapshot&&preview.body.selections.some(item=>item.asset_id==='mixed'&&item.mode==='disabled'&&item.version==='asset-v2'));

  await context.getByRole('button',{name:'读取 Skill 上下文',exact:true}).click();await context.locator('.skill-input-evidence').first().waitFor();
  const latest=context.locator('.latest-skill-frame'),expected=responses[contextPath].frames.at(-1).input_evidence;
  check('original_operation_and_snapshot_visible',(await latest.innerText()).includes(expected.operation_id)&&(await latest.innerText()).includes(expected.snapshot_id));
  check('requested_body_load_verified',await latest.getByText('请求版本正文已载入',{exact:true}).count()===1);
  await latest.getByText('请求正文摘要',{exact:true}).first().click();
  check('requested_file_digest_visible',(await latest.innerText()).includes(expected.items[0].expected_sha256));
  check('loading_not_method_use',(await latest.innerText()).includes('不代表模型已使用该方法'));
  const old=responses[contextPath].frames.find(frame=>frame.input_evidence?.items.some(item=>item.asset_version==='asset-v1'));
  const historical=context.locator('.skill-context-frame').filter({hasText:old.turn_id});await historical.locator('summary').first().click();
  check('historical_request_keeps_old_version',(await historical.innerText()).includes('请求资产版本 asset-v1'));
  check('no_skill_body_exposure',!(await context.innerText()).includes('MIXED_NATIVE_BODY')&&!(await context.innerText()).includes('SECOND_NATIVE_BODY'));
  await latest.locator('.skill-input-evidence').scrollIntoViewIfNeeded();await page.screenshot({path:screenshotRoot+'/desktop.png',fullPage:true});
  mismatch=true;await context.getByRole('button',{name:'读取 Skill 上下文',exact:true}).click();await latest.getByText('载入正文与请求版本不一致',{exact:true}).waitFor();
  check('mismatch_not_verified',await latest.getByText('请求版本正文已载入',{exact:true}).count()===0);
  await page.getByRole('button',{name:'＋ 新建',exact:true}).click();
  const choice=page.locator('select[name="capability_selection:mixed"]');await choice.waitFor();
  check('shared_skill_creation_defaults_to_inherit',await choice.inputValue()==='inherit');
  check('creation_explains_native_source_boundary',(await page.locator('.capability-references').innerText()).includes('已有原生入口及其路径别名会拒绝'));
  await choice.selectOption('enabled');await page.getByRole('button',{name:'创建会话',exact:true}).click();
  await page.locator('.composer').waitFor();
  const creation=requests.find(request=>request.path==='/api/commands'&&request.body?.kind==='create')?.body;
  check('creation_uses_shared_asset_id',creation?.capability_selections?.some(item=>item.asset_id==='mixed'&&item.mode==='enabled'));
  check('creation_does_not_supply_delivery_paths',!JSON.stringify(creation).includes('cache_path')&&!JSON.stringify(creation).includes('skill_asset'));
  if(await picker.getAttribute('open')===null)await picker.locator('summary').click();
  await first.check();responses[sessionPath].session.execution_snapshot.id='changed-snapshot';responses[sessionPath].sequence+=100;responses['/api/workspace'].sequence+=100;
  await page.evaluate(()=>window.dispatchEvent(new Event('online')));await page.waitForFunction(()=>![...document.querySelectorAll('.skill-input-picker input')].some(input=>input.checked));
  check('snapshot_change_clears_old_choice',!await first.isChecked());
  // Extra options exercise the UI limit only; they are not native fixtures.
  const pack=responses[sessionPath].session.execution_snapshot.plugin_selections.find(pack=>pack.mode==='enabled');
  for(let i=0;i<16;i++)pack.components.push({kind:'skill',native_name:'limit:'+i,name:'limit-'+i,path:'skills/limit-'+i+'/SKILL.md',count:1});
  responses[sessionPath].sequence+=100;responses['/api/workspace'].sequence+=100;
  await page.evaluate(()=>window.dispatchEvent(new Event('online')));
  await page.waitForFunction(()=>document.querySelectorAll('.skill-input-picker input').length===17);
  const choices=picker.getByRole('checkbox');for(let i=0;i<16;i++)await choices.nth(i).check();
  check('sixteen_item_limit',await choices.nth(16).isDisabled()&&await choices.nth(0).isEnabled());
  await choices.nth(0).uncheck();check('deselect_reopens_remaining_choice',await choices.nth(16).isEnabled());
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await context.scrollIntoViewIfNeeded();
  check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  if(await context.getAttribute('open')===null)await context.locator('summary').first().click();
  await context.getByRole('button',{name:'读取 Skill 上下文',exact:true}).click();
  await latest.locator('.skill-input-evidence').scrollIntoViewIfNeeded();await page.screenshot({path:screenshotRoot+'/mobile.png',fullPage:true});
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));
  if(await context.getAttribute('open')===null)await context.locator('summary').first().click();
  await context.getByText('工作台已断开，连接后可重新读取。',{exact:true}).waitFor();
  check('offline_clears_read_evidence',await context.locator('.skill-input-evidence').count()===0);
  check('offline_disables_skill_selection',await first.isDisabled());
  check('no_page_errors',errors.length===0);
  return {checks,requests,errors,scope:'built UI with native shared Skill Handler exports; controlled HTTP loss, preview payload boundary, snapshot drift and body mismatch; no extra native/model input'};
}
