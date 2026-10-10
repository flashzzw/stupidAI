// Built UI against real Runtime/Application/Handler read results; HTTP/SSE controlled.
async page => {
  page.setDefaultTimeout(4000);await page.unrouteAll({behavior:'ignoreErrors'});
  const fixture=__FIXTURE__,assets=__ASSETS__,sid=fixture.sessions[0];
  const path='/api/sessions/'+sid+'/skills/context';
  const report=fixture.responses[path],checks={},requests=[],errors=[],consoleErrors=[];let fail=false;
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  page.on('pageerror',e=>errors.push(e.message));
  page.on('console',m=>{if(m.type()==='error')consoleErrors.push(m.text());});
  await page.addInitScript(id=>{sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};},sid);
  await page.route('**/*',async route=>{
    const r=route.request(),p=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=r.postData()?JSON.parse(r.postData()):null;requests.push({path:p,method:r.method(),body});
    if(p===path){check('read_uses_empty_body',r.method()==='POST'&&JSON.stringify(body)==='{}');return fail?route.fulfill({status:409,json:{error:'原生 Skill 上下文读取失败'}}):route.fulfill({json:report});}
    if(fixture.responses[p])return route.fulfill({json:fixture.responses[p]});
    if(assets[p])return route.fulfill({path:assets[p],contentType:p.endsWith('.js')?'application/javascript':p.endsWith('.css')?'text/css':p.endsWith('.svg')?'image/svg+xml':'text/html'});
    return route.fulfill({status:404,body:'unmapped fixture path'});
  });
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9467/');await page.locator('.execution-snapshot>summary').click();
  const section=page.locator('.skill-context');await section.waitFor();
  check('no_automatic_context_read',!requests.some(r=>r.path===path));
  await section.locator('summary').first().click();await section.getByRole('button',{name:'读取 Skill 上下文',exact:true}).click();
  await section.getByText('最近记录的上下文 · 2 个轮次片段',{exact:true}).waitFor();
  const latest=section.locator('.latest-skill-frame');
  check('latest_turn_bound', (await latest.innerText()).includes(report.frames[1].turn_id));
  check('disabled_not_in_latest_descriptions',!(await latest.innerText()).includes('fixture:method'));
  check('latest_has_no_new_body_load',(await latest.innerText()).includes('此片段没有新增正文载入记录'));
  const old=section.locator('.skill-context-frame').nth(1);await old.locator('summary').first().click();
  check('historical_description_visible',(await old.innerText()).includes('W0208_CONTEXT_DESCRIPTION'));
  check('historical_body_load_visible',(await old.innerText()).includes('正文已载入上下文'));
  check('native_source_path_visible',(await old.innerText()).includes(report.frames[0].loads[0].path));
  check('body_hash_not_file_version',(await old.innerText()).includes('载入片段摘要'));
  check('body_contents_not_exposed',!(await section.innerText()).includes('PRIVATE_NATIVE_SKILL_BODY'));
  check('method_and_current_state_not_claimed',(await section.innerText()).includes('不证明模型已收到、方法已使用或 Gate 通过')&&(await section.innerText()).includes('改配后尚无新轮次时'));
  await old.getByText('fixture:method',{exact:true}).scrollIntoViewIfNeeded();await page.screenshot({path:'.dev/w0208-skill-context-view/desktop.png',fullPage:true});
  fail=true;await section.getByRole('button',{name:'读取 Skill 上下文',exact:true}).click();await section.getByRole('alert').waitFor();
  check('failure_clears_previous_report',await section.locator('.skill-context-frame').count()===0);
  fail=false;await section.getByRole('button',{name:'读取 Skill 上下文',exact:true}).click();await section.getByText('最近记录的上下文 · 2 个轮次片段',{exact:true}).waitFor();
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await section.scrollIntoViewIfNeeded();
  const mobileOld=section.locator('.skill-context-frame').nth(1);await mobileOld.locator('summary').first().click();await mobileOld.getByText('fixture:method',{exact:true}).scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));await page.screenshot({path:'.dev/w0208-skill-context-view/mobile.png',fullPage:true});
  await page.setViewportSize({width:1440,height:1100});
  const previousReads=requests.filter(r=>r.path===path).length;
  await page.locator('.session-row').filter({hasText:'No prepared context'}).click();await page.locator('.execution-snapshot>summary').click();
  check('session_switch_clears_previous_report',await section.locator('.skill-context-frame').count()===0);
  check('session_switch_does_not_read_context',requests.filter(r=>r.path===path).length===previousReads);
  await page.locator('.session-row').filter({hasText:'Skill context history'}).click();await page.locator('.execution-snapshot>summary').click();
  await section.locator('summary').first().click();await section.getByRole('button',{name:'读取 Skill 上下文',exact:true}).click();await section.getByText('最近记录的上下文 · 2 个轮次片段',{exact:true}).waitFor();
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));
  if(await section.getAttribute('open')===null)await section.locator('summary').first().click();
  check('disconnect_disables_context_read',await section.getByRole('button',{name:'读取 Skill 上下文',exact:true}).isDisabled());
  check('disconnect_clears_previous_report',await section.locator('.skill-context-frame').count()===0);
  check('no_native_commands_from_browser',!requests.some(r=>r.path==='/api/commands'));
  check('no_uncaught_page_errors',errors.length===0);
  check('only_injected_http_failure_in_console',consoleErrors.every(e=>e.includes('409')));
  return {checks,requests,errors,consoleErrors,scope:'built UI using real native read responses; controlled HTTP/SSE, no additional native work'};
}
