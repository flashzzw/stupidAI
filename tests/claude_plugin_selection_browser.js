// Built UI replay of real Application/Runtime/native Session exports. Browser
// transport and create receipt delivery are controlled, not a live HTTP server.
async page => {
  await page.unrouteAll({behavior:'ignoreErrors'});
  const fixture=__FIXTURE__, assets=__ASSETS__, checks={}, requests=[], errors=[];
  const screenshotPrefix='.dev/w0208-plugins/selection';
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw new Error(name);};
  page.__pluginSelection={checks,requests,errors};
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{
    sessionStorage.setItem('netcap.selected.v1',id);
    window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};
  },fixture.sessions.a);
  await page.route('**/*',async route=>{
    const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];
    const body=r.postData()?JSON.parse(r.postData()):null;requests.push({path,method:r.method(),body});
    if(path==='/api/commands'&&body?.kind==='create')return route.fulfill({status:202,json:{command_id:body.command_id,status:'accepted',session_id:fixture.sessions.a}});
    if(fixture.responses[path])return route.fulfill({json:fixture.responses[path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'unmapped fixture route'});
  });
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9455/');
  await page.locator('.execution-snapshot>summary').click();
  const section=page.getByRole('region',{name:'本次 CLI 插件选择'});
  await section.getByText('fixture-plugin · 启用整包',{exact:true}).waitFor();
  check('enabled_verified',(await section.innerText()).includes('原生包与组件清单匹配'));
  check('source_key_and_manifest_version',(await section.innerText()).includes('fixture-plugin@inline')&&(await section.innerText()).includes('1.2.3'));
  check('asset_version_separate',(await page.locator('.execution-snapshot').innerText()).includes('asset-v1'));
  await section.getByText('包内组件 · 4 项',{exact:true}).click();
  const componentText=await section.innerText();
  check('component_native_names',['fixture-plugin:method-name','fixture-plugin:action','fixture-plugin:verifier','PreToolUse'].every(name=>componentText.includes(name)));
  check('hook_execution_not_claimed',componentText.includes('不代表 hook 执行、工具调用或方法使用成功'));
  check('plugin_change_entry_visible',componentText.includes('原会话能力改配'));
  check('no_private_bodies',!componentText.includes('PRIVATE_'));
  await page.locator('.session-row').filter({hasText:'Plugin B'}).click();
  await page.locator('.execution-snapshot>summary').click();
  await section.getByText('fixture-plugin · 禁用整包',{exact:true}).waitFor();
  check('disabled_verified',(await section.innerText()).includes('原生包与组件清单匹配'));
  await section.scrollIntoViewIfNeeded();
  await page.screenshot({path:screenshotPrefix+'-1440.png',fullPage:true});
  await page.getByRole('button',{name:'＋ 新建',exact:true}).click();
  const select=page.locator('select[name="capability_selection:plugin"]');
  await select.waitFor();check('new_selection_defaults_to_inherit',await select.inputValue()==='inherit');
  await select.selectOption('disabled');
  check('single_asset_choice_available',await select.inputValue()==='disabled');
  check('selection_limits_visible',(await page.locator('.capability-references').innerText()).includes('同名已安装来源或无法核对的组件会拒绝'));
  await page.getByRole('button',{name:'创建会话',exact:true}).click();
  const created=requests.find(r=>r.path==='/api/commands'&&r.body?.kind==='create');
  check('choice_serialized_by_asset_id',created?.body?.capability_selections?.some(s=>s.asset_id==='plugin'&&s.mode==='disabled'));
  check('client_does_not_supply_native_source',!JSON.stringify(created?.body).includes('native_key')&&!JSON.stringify(created?.body).includes('source_path'));
  await page.setViewportSize({width:390,height:844});
  await page.getByRole('button',{name:'详情',exact:true}).click();
  if(await page.locator('.execution-snapshot').getAttribute('open')===null)await page.locator('.execution-snapshot>summary').click();
  await section.scrollIntoViewIfNeeded();
  await page.screenshot({path:screenshotPrefix+'-390.png',fullPage:true});
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));
  await section.getByText('已断开，原生状态待重新核对',{exact:true}).waitFor();
  check('disconnection_does_not_claim_current_match',await section.getByText('原生包与组件清单匹配',{exact:true}).count()===0);
  check('no_browser_errors',errors.length===0);
  return {checks,requests,errors,scope:'built UI + real native Application exports; controlled HTTP/create receipt; no additional native creation or input'};
}
