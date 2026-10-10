// Real Application observations replayed through a controlled HTTP transport.
async page => {
  await page.unrouteAll({behavior:'ignoreErrors'});
  const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
  let phase='before';
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  page.__fileAlias={checks,requests,errors};
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{
    sessionStorage.setItem('netcap.selected.v1',id);
    window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};
  },fixture.sessions.a);
  await page.route('**/*',async route=>{
    const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];
    requests.push({phase,path,method:r.method()});
    if(fixture.responses[phase][path])return route.fulfill({json:fixture.responses[phase][path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'unmapped fixture route'});
  });
  await page.setViewportSize({width:1440,height:1100});
  const section=page.getByRole('region',{name:'本次原生 Skill 选择'});
  for(phase of ['before','blocked','repaired']){
    await page.goto('http://127.0.0.1:9455/');
    for(const label of ['a','b']){
      const sid=fixture.sessions[label],saved=fixture.responses.before['/api/sessions/'+sid].session;
      await page.locator('.session-row').filter({hasText:saved.title}).click();
      if(await page.locator('.execution-snapshot').getAttribute('open')===null)await page.locator('.execution-snapshot>summary').click();
      const selected=saved.execution_snapshot.skill_selections[0];
      await section.getByText(selected.name+' · '+(selected.mode==='enabled'?'启用':'禁用'),{exact:true}).waitFor();
      const content=await section.innerText();
      check(phase+'_'+label+'_original_identity',(await page.locator('.inspector').innerText()).includes(saved.scope.native_id));
      check(phase+'_'+label+'_original_source_and_mode',content.includes(selected.discovery_path)&&content.includes('原生目录键：'+selected.native_key));
      check(phase+'_'+label+'_verification_visible',content.includes(phase==='blocked'?'原生选择待核对':'原生命令与描述清单匹配'));
      await page.getByLabel('消息',{exact:true}).fill('UI verification only; never submit');
      const send=page.getByRole('button',{name:/发送消息/});
      check(phase+'_'+label+'_input_state',phase==='blocked'?await send.isDisabled():await send.isEnabled());
      check(phase+'_'+label+'_method_gate_not_claimed',content.includes('不代表正文已加载、方法已使用或 Gate 已通过'));
      if(label==='b'&&phase!=='before'){
        await section.scrollIntoViewIfNeeded();
        await page.screenshot({path:'.dev/w0207-claude-file-alias/'+phase+'-1440.png',fullPage:true});
      }
    }
    if(phase==='blocked'){
      await page.setViewportSize({width:390,height:844});
      await page.getByRole('button',{name:'详情',exact:true}).click();
      await section.scrollIntoViewIfNeeded();
      check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
      await page.screenshot({path:'.dev/w0207-claude-file-alias/blocked-390.png',fullPage:true});
      await page.setViewportSize({width:1440,height:1100});
    }
  }
  check('no_execution_commands',requests.every(r=>r.path!=='/api/commands'));
  check('no_browser_errors',errors.length===0);
  return {checks,requests,errors,scope:'built UI + actual Application/SQLite/native Session observations; HTTP replay only; no submitted input'};
}
