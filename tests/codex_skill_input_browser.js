// Built UI with native Handler data; HTTP loss, snapshot drift and evidence
// mismatch are controlled browser cases, not additional native execution.
async page => {
  page.setDefaultTimeout(5000);await page.unrouteAll({behavior:'ignoreErrors'});
  const fixture=__FIXTURE__,assets=__ASSETS__,sid=fixture.session,responses=JSON.parse(JSON.stringify(fixture.responses));
  const screenshotRoot=fixture.portable_layout?'.dev/w0208-portable-'+fixture.portable_layout:fixture.custom_skill_roots?'.dev/w0208-custom-skills':fixture.skill_only?'.dev/w0208-skill-only':'.dev/w0207-skill-input';
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
      check('only_message_input_submitted',body?.kind==='send');
      if(lose){lose=false;return route.abort('failed');}
      return route.fulfill({status:202,json:{command_id:body.command_id,session_id:sid,status:'accepted'}});
    }
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
  const first=picker.getByRole('checkbox',{name:/mixed:method/}),second=picker.getByRole('checkbox',{name:/mixed:second/});
  check('skills_opt_in',!await first.isChecked()&&!await second.isChecked());
  check('saved_asset_version_visible',(await picker.innerText()).includes('资产版本：asset-v2'));
  await first.check();await second.check();await page.locator('#message').fill('Use both selected methods.');
  const send=page.getByRole('button',{name:'发送消息'});await send.click();await page.getByRole('alert').waitFor();
  check('lost_http_receipt_keeps_draft',await page.locator('#message').inputValue()==='Use both selected methods.');
  check('lost_http_receipt_keeps_skill_choices',await first.isChecked()&&await second.isChecked());
  await send.click();await page.waitForFunction(()=>document.querySelector('#message').value==='');
  const commands=requests.filter(r=>r.path==='/api/commands');
  check('retry_uses_same_command_id',commands.length===2&&commands[0].body.command_id===commands[1].body.command_id);
  const command=commands[1].body;
  check('request_pins_visible_snapshot',command.skill_input?.snapshot_id===originalSnapshot);
  check('two_asset_name_pairs_serialized',command.skill_input?.items.length===2&&command.skill_input.items[0].asset_id==='mixed'&&command.skill_input.items[0].name==='mixed:method'&&command.skill_input.items[1].name==='mixed:second');
  check('browser_cannot_choose_native_path',command.skill_input.items.every(item=>Object.keys(item).sort().join(',')==='asset_id,name'));
  check('accepted_request_clears_choices',!await first.isChecked()&&!await second.isChecked());
  await page.locator('.execution-snapshot>summary').click();const context=page.locator('.skill-context');await context.locator('summary').first().click();
  if(fixture.portable_layout){
    const pack=responses[sessionPath].session.execution_snapshot.plugin_selections[0];
    const selected=page.getByRole('region',{name:'本次 CLI 插件选择'});
    const source=selected.locator('.plugin-manifest-source');
    check('portable_root_identity_visible',(await source.innerText()).includes('身份文件：plugin.json'));
    check('portable_selected_settings_visible',(await source.innerText()).includes('Codex 配置来源：'+pack.manifest_source.settings));
    check('portable_declared_version_visible',(await selected.innerText()).includes('插件声明版本：2.0.0'));
    check('portable_overlay_identity_not_claimed',!(await selected.innerText()).includes('ignored-identity')&&!(await selected.innerText()).includes('9.9.9'));
    await selected.locator('details>summary').click();
    const components=await selected.innerText();
    check('portable_canonical_mcp_visible',components.includes('mcp.json')&&components.includes('mixed_service_v2')&&!components.includes('ignored_mcp'));
    check('portable_ignored_skill_not_selectable',await picker.getByRole('checkbox').count()===2&&!(await picker.innerText()).includes('ignored'));
    if(fixture.portable_layout==='fallback')check('portable_fallback_keeps_canonical_mcp',!components.includes('selected_service_v2')&&!components.includes('selected-mcp.json'));
  }
  if(fixture.custom_skill_roots){
    const selected=page.getByRole('region',{name:'本次 CLI 插件选择'});
    await selected.locator('details>summary').click();
    const components=responses[sessionPath].session.execution_snapshot.plugin_selections[0].components;
    const text=await selected.innerText();
    check('custom_skill_paths_visible',components.filter(c=>c.kind==='skill').every(c=>text.includes(c.path)));
    check('custom_skill_support_explained',text.includes('插件声明的自定义目录'));
    check('replaced_default_not_selectable',await picker.getByRole('checkbox').count()===2&&!(await picker.innerText()).includes('mixed:ignored'));
  }
  if(fixture.skill_only){
    const selected=page.getByRole('region',{name:'本次 CLI 插件选择'});
    check('skill_only_policy_coverage_visible',(await selected.innerText()).includes('固定插件包与整包策略已核对'));
    check('skill_only_no_mcp_claim',!(await selected.innerText()).includes('MCP 与整包策略已核对'));
    check('skill_only_components',responses[sessionPath].session.execution_snapshot.plugin_selections.every(pack=>pack.components.every(component=>component.kind==='skill')));
  }
  await context.getByRole('button',{name:'读取 Skill 上下文',exact:true}).click();await context.locator('.skill-input-evidence').first().waitFor();
  const latest=context.locator('.latest-skill-frame'),expected=responses[contextPath].frames.at(-1).input_evidence;
  check('original_operation_and_snapshot_visible',(await latest.innerText()).includes(expected.operation_id)&&(await latest.innerText()).includes(expected.snapshot_id));
  check('both_body_loads_verified',await latest.getByText('请求版本正文已载入',{exact:true}).count()===2);
  await latest.getByText('请求正文摘要',{exact:true}).first().click();
  check('requested_file_digest_visible',(await latest.innerText()).includes(expected.items[0].expected_sha256));
  check('loading_not_method_use',(await latest.innerText()).includes('不代表模型已使用该方法'));
  const old=responses[contextPath].frames.find(frame=>frame.input_evidence?.items.some(item=>item.asset_version==='asset-v1'));
  const historical=context.locator('.skill-context-frame').filter({hasText:old.turn_id});await historical.locator('summary').first().click();
  check('historical_request_keeps_old_version',(await historical.innerText()).includes('请求资产版本 asset-v1'));
  if(fixture.custom_skill_roots){
    const latestText=await latest.innerText(),oldText=await historical.innerText();
    check('upgraded_skill_load_paths_visible',expected.items.every(item=>latestText.includes(item.path)));
    check('historical_skill_load_paths_preserved',old.input_evidence.items.every(item=>oldText.includes(item.path)));
  }
  check('no_skill_body_exposure',!(await context.innerText()).includes('MIXED_NATIVE_BODY')&&!(await context.innerText()).includes('SECOND_NATIVE_BODY'));
  await latest.locator('.skill-input-evidence').scrollIntoViewIfNeeded();await page.screenshot({path:screenshotRoot+'/desktop.png',fullPage:true});
  mismatch=true;await context.getByRole('button',{name:'读取 Skill 上下文',exact:true}).click();await latest.getByText('载入正文与请求版本不一致',{exact:true}).waitFor();
  check('mismatch_not_verified',await latest.getByText('请求版本正文已载入',{exact:true}).count()===1);
  await first.check();responses[sessionPath].session.execution_snapshot.id='changed-snapshot';responses[sessionPath].sequence+=100;responses['/api/workspace'].sequence+=100;
  await page.evaluate(()=>window.dispatchEvent(new Event('online')));await page.waitForFunction(()=>![...document.querySelectorAll('.skill-input-picker input')].some(input=>input.checked));
  check('snapshot_change_clears_old_choice',!await first.isChecked());
  // Extra options exercise the UI limit only; they are not native fixtures.
  const pack=responses[sessionPath].session.execution_snapshot.plugin_selections.find(pack=>pack.mode==='enabled');
  for(let i=0;i<15;i++)pack.components.push({kind:'skill',native_name:'limit:'+i,name:'limit-'+i,path:'skills/limit-'+i+'/SKILL.md',count:1});
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
  return {checks,requests,errors,scope:'built UI with native Handler exports; controlled lost HTTP receipt, changed snapshot and mismatched body evidence; no extra native/model input'};
}
