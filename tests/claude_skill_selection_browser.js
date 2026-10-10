// Built UI replay of real Application/Runtime/native Session exports. Browser
// transport and create receipt delivery are controlled, not a live HTTP server.
async page => {
  await page.unrouteAll({behavior:'ignoreErrors'});
  const fixture=__FIXTURE__, assets=__ASSETS__, checks={}, requests=[], errors=[];
  const selected=id=>fixture.responses['/api/snapshot'].sessions.find(s=>s.id===id).execution_snapshot.skill_selections[0];
  const pickA=selected(fixture.sessions.a),pickB=selected(fixture.sessions.b);
  const displayName=s=>s.plugin_name?s.name.slice(s.plugin_name.length+1):s.name;
  const external=!!pickA.delivery, screenshotPrefix=external?'.dev/w0207-resources/selection':'.dev/w0207-claude/selection';
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw new Error(name);};
  page.__skillSelection={checks,requests,errors};
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
  const section=page.getByRole('region',{name:'本次原生 Skill 选择'});
  await section.getByText(displayName(pickA)+' · 启用',{exact:true}).waitFor();
  check('enabled_bound_skill_visible',(await section.innerText()).includes('原生命令与描述清单匹配'));
  check('native_key_distinct_from_declared_name',(await section.innerText()).includes('原生目录键：'+pickA.native_key));
  check('profile_entry_visible',(await section.innerText()).includes(pickA.discovery_path));
  check('method_gate_not_claimed',(await section.innerText()).includes('不代表正文已加载、方法已使用或 Gate 已通过'));
  await page.locator('.session-row').filter({hasText:'Skill B'}).click();
  await page.locator('.execution-snapshot>summary').click();
  await section.getByText(displayName(pickB)+' · 禁用',{exact:true}).waitFor();
  check('disabled_native_inventory_visible',(await section.innerText()).includes('命令清单：未报告 · 描述清单：未报告'));
  check('other_profile_binding_visible',(await section.innerText()).includes(pickB.discovery_path));
  if(external){
    check('disabled_package_explained',(await section.innerText()).includes('本会话省略该 Skill 包'));
    check('registered_source_visible',(await section.innerText()).includes(pickB.source_path));
    check('shared_asset_independent_namespace',pickA.plugin_name!==pickB.plugin_name);
  }
  await section.scrollIntoViewIfNeeded();
  await page.screenshot({path:screenshotPrefix+'-1440.png',fullPage:true});
  await page.getByRole('button',{name:'＋ 新建',exact:true}).click();
  const select=page.locator('select[name="capability_selection:shared"]');
  await select.waitFor();check('new_selection_defaults_to_inherit',await select.inputValue()==='inherit');
  await select.selectOption('disabled');
  check('single_asset_choice_available',await select.inputValue()==='disabled');
  check('native_and_external_scope_visible',(await page.locator('.capability-references').innerText()).includes('已有的用户/项目 Skill 入口'));
  check('disabled_does_not_revoke_history',(await page.locator('.capability-references').innerText()).includes('禁用不抹除历史或撤销文件权限'));
  await page.getByRole('button',{name:'创建会话',exact:true}).click();
  const created=requests.find(r=>r.path==='/api/commands'&&r.body?.kind==='create');
  check('choice_serialized_by_asset_id',created?.body?.capability_selections?.some(s=>s.asset_id==='shared'&&s.mode==='disabled'));
  check('client_does_not_supply_native_source',!JSON.stringify(created?.body).includes('native_key')&&!JSON.stringify(created?.body).includes('source_path'));
  await page.setViewportSize({width:390,height:844});
  await page.getByRole('button',{name:'详情',exact:true}).click();
  if(await page.locator('.execution-snapshot').getAttribute('open')===null)await page.locator('.execution-snapshot>summary').click();
  await section.scrollIntoViewIfNeeded();
  await page.screenshot({path:screenshotPrefix+'-390.png',fullPage:true});
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  check('no_browser_errors',errors.length===0);
  return {checks,requests,errors,scope:'built UI + real native Application exports; controlled HTTP/create receipt; no additional native creation or input'};
}
