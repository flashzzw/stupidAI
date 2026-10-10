// Replay actual Application/Handler responses; no CLI or mutation requests.
async page => {
  const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
  const id=fixture.samples.initial.id;
  let phase='initial',mode='native';
  const check=(name,value)=>{checks[name]=!!value;if(!value)throw Error(name);};
  const clone=value=>JSON.parse(JSON.stringify(value));
  page.setDefaultTimeout(5000);await page.unrouteAll({behavior:'ignoreErrors'});
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{
    sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);
    window.EventSource=class{
      constructor(){this.listeners={};window.userHookEvents=this;setTimeout(()=>this.onopen?.(),0);}
      addEventListener(name,fn){this.listeners[name]=fn;}close(){}
    };
  },id);
  await page.route('**/*',async route=>{
    const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];
    requests.push({path,method:r.method()});
    if(path==='/favicon.ico')return route.fulfill({status:204,body:''});
    const row=fixture.responses[phase][path];
    if(row){
      const body=clone(row);
      if(path==='/api/sessions/'+id&&mode!=='native'){
        if(mode==='legacy')delete body.session.execution_snapshot.codex_user_hooks;
        if(mode==='absent')body.session.execution_snapshot.codex_user_hooks.file={...body.session.execution_snapshot.codex_user_hooks.file,kind:'absent',sha256:undefined,mode:undefined};
      }
      return route.fulfill({json:body});
    }
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,json:{error:'unmapped fixture path'}});
  });
  await page.setViewportSize({width:1440,height:1000});await page.goto('http://127.0.0.1:9463/');
  const execution=page.locator('.execution-snapshot');
  await execution.locator('summary').first().click();
  let source=page.locator('.codex-user-hook-source');await source.locator('summary').click();
  const expected=fixture.samples.initial.execution_snapshot.codex_user_hooks.file;
  let text=await source.innerText();
  check('recorded_path',text.includes(expected.path));
  check('recorded_hash',text.includes(expected.sha256));
  check('recorded_mode',text.includes('600'));
  check('coverage_explicit',text.includes('不表示已加载或已执行')&&text.includes('项目、受管 hook 和引用脚本'));
  check('command_body_not_disclosed',!text.includes('OWNED_V1'));
  check('no_automatic_source_check',!requests.some(r=>r.path.endsWith('/execution')));
  await page.getByRole('button',{name:'核对当前来源',exact:true}).click();
  await page.getByText('已记录的来源与绑定未发现变化。',{exact:true}).waitFor();
  check('initial_source_check',true);
  await page.screenshot({path:'.dev/w0209-codex-hook-sources/initial-1440.png',fullPage:true});
  phase='drifted';await page.evaluate(()=>window.userHookEvents.listeners.change?.());
  await page.getByRole('button',{name:'核对当前来源',exact:true}).click();
  await page.getByText('来源或绑定已变化，继续前需核对。',{exact:true}).waitFor();
  text=await execution.innerText();
  check('drift_diagnostic',text.includes('Codex 用户 hook')&&text.includes('已变化'));
  check('old_record_kept',text.includes(expected.sha256));
  await page.locator('.composer textarea').fill('Owned draft; never sent');
  check('input_disabled',await page.getByRole('button',{name:'发送消息'}).isDisabled());
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await source.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await page.screenshot({path:'.dev/w0209-codex-hook-sources/drift-390.png',fullPage:true});
  phase='restored';await page.evaluate(()=>window.userHookEvents.listeners.change?.());
  await page.getByRole('button',{name:'核对当前来源',exact:true}).click();
  await page.getByText('已记录的来源与绑定未发现变化。',{exact:true}).waitFor();
  check('restored_source_check',true);
  mode='legacy';await page.evaluate(()=>window.userHookEvents.listeners.change?.());
  await source.getByText('此快照未记录用户 hook 文件版本；不会补造历史来源。',{exact:true}).waitFor();
  check('legacy_no_fabricated_source',!(await source.innerText()).includes(expected.sha256));
  mode='absent';await page.evaluate(()=>window.userHookEvents.listeners.change?.());
  await source.getByText('记录时不存在',{exact:true}).waitFor();
  check('absent_distinguished',!(await source.innerText()).includes('记录的文件权限'));
  check('no_mutations',!requests.some(r=>r.method!=='GET'));
  check('no_browser_errors',errors.length===0);
  return {checks,passed:Object.values(checks).every(Boolean),requests,errors,scope:'native Handler response replay plus explicit legacy/absent rendering fixtures; no native execution'};
}
