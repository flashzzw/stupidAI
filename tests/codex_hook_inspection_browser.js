// Built UI with actual Application/Handler/native metadata exports; controlled HTTP/SSE.
async page => {
  page.setDefaultTimeout(5000);
  await page.unrouteAll({behavior:'ignoreErrors'});
  const fixture=__FIXTURE__, assets=__ASSETS__, checks={}, requests=[], errors=[];
  const check=(name,value)=>{checks[name]=!!value;if(!value)throw Error(name);};
  const endpoint='/api/capabilities/codex-hooks';
  let mode='normal', updated=false, pending, release;
  const count=()=>requests.filter(r=>r.path===endpoint).length;
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(()=>{
    sessionStorage.clear();
    window.EventSource=class {
      constructor(){this.listeners={};window.inspectionEvents=this;setTimeout(()=>this.onopen?.(),0);}
      addEventListener(name,handler){this.listeners[name]=handler;}
      close(){}
    };
  });
  await page.route('**/*',async route=>{
    const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=r.postData()?JSON.parse(r.postData()):null;
    requests.push({path,method:r.method(),body});
    if(path===endpoint){
      check('registered_identifiers_only',Object.keys(body).sort().join(',')==='asset_id,instance_id,revision,source_sha256'&&r.method()==='POST');
      if(mode==='failure')return route.fulfill({status:409,json:fixture.drift_error});
      const result=JSON.parse(JSON.stringify(updated?fixture.updated_inspection:body.instance_id===fixture.alias.request.instance_id?fixture.alias:fixture.inspection));
      if(mode==='mismatch')result.request.source_sha256='stale-response';
      if(mode==='delayed')await new Promise(resolve=>{release=resolve;});
      await route.fulfill({json:result});
      pending?.(); pending=undefined;
      return;
    }
    if(path==='/api/workspace')return route.fulfill({json:updated?fixture.updated_snapshot:fixture.snapshot});
    if(path==='/api/capabilities')return route.fulfill({json:updated?fixture.updated_capabilities:fixture.capabilities});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,json:{error:'unmapped fixture route'}});
  });
  await page.setViewportSize({width:1440,height:1100});
  await page.goto('http://127.0.0.1:9462/');
  await page.getByRole('button',{name:'能力库',exact:true}).click();
  const panel=page.locator('.plugin-hook-inspection');
  const open=async()=>{await panel.waitFor();if(await panel.getAttribute('open')===null)await panel.locator('summary').first().click();};
  const read=()=>panel.getByRole('button',{name:'读取原生 hook 定义',exact:true}).click();
  const ready=version=>panel.getByRole('status').filter({hasText:`包定义已核对 · ${version} · 4 个 handler`}).waitFor();
  await open();
  check('no_automatic_inspection',count()===0);
  await read();await ready('asset-v1');
  let text=await panel.innerText();
  check('four_complete_handlers',await panel.locator('.snapshot-asset').count()===4);
  check('native_event_timeout_matcher',['preToolUse','sessionStart','sessionEnd','期限 4 秒','匹配 Read'].every(s=>text.includes(s)));
  check('relative_package_paths',text.includes('hooks/hooks.json')&&text.includes('custom.json')&&!text.includes('/tmp/'));
  check('async_is_declaration',text.includes('异步声明')&&text.includes('同步声明'));
  check('package_scope_not_trust_or_execution',text.includes('此结果不表示已授信、已选配或执行成功')&&text.includes('原生摘要不包含脚本正文'));
  check('no_command_or_output',!text.includes('PRIVATE_')&&!text.includes('printf')&&!text.includes('exec ')&&!text.includes('标准输出'));
  await panel.locator('.snapshot-asset details summary').first().click();
  text=await panel.innerText();
  check('exact_native_key_hash',text.includes(fixture.inspection.inspection.definitions[0].key)&&text.includes(fixture.inspection.inspection.definitions[0].definition_hash));
  check('desktop_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.screenshot({path:'.dev/w0208-hook-inspection/inspection-1440.png',fullPage:true});
  let before=count();
  await panel.getByRole('combobox').selectOption(fixture.alias.request.instance_id);
  check('instance_switch_clears_result',await panel.locator('.snapshot-asset').count()===0);
  check('instance_switch_does_not_read',count()===before);
  await read();await ready('asset-v1');
  check('selected_instance_sent',requests.filter(r=>r.path===endpoint).at(-1).body.instance_id===fixture.alias.request.instance_id);
  mode='failure';await read();await panel.getByRole('alert').waitFor();
  check('conflict_clears_result',await panel.locator('.snapshot-asset').count()===0);
  check('conflict_reason_visible',(await panel.innerText()).includes(fixture.drift_error.error));
  mode='mismatch';await read();await panel.getByRole('alert').waitFor();
  check('mismatched_response_rejected',(await panel.innerText()).includes('与本次包版本或实例不一致')&&await panel.locator('.snapshot-asset').count()===0);
  mode='delayed';const delivered=new Promise(resolve=>{pending=resolve;});
  await read();await panel.getByRole('button',{name:'正在核对包定义…'}).waitFor();
  check('duplicate_read_disabled',await panel.getByRole('button',{name:'正在核对包定义…'}).isDisabled());
  await panel.getByRole('combobox').selectOption(fixture.request.instance_id);
  while(!release)await new Promise(resolve=>setTimeout(resolve,10));
  release();await delivered;
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  check('late_other_instance_result_ignored',await panel.locator('.snapshot-asset').count()===0);
  mode='normal';await read();await ready('asset-v1');
  before=count();updated=true;
  await page.evaluate(()=>window.inspectionEvents.listeners.change?.());
  await page.locator('.capability-card>.muted').filter({hasText:'asset-v2'}).waitFor();
  await open();
  check('catalog_revision_clears_result',await panel.locator('.snapshot-asset').count()===0);
  check('catalog_revision_does_not_read',count()===before);
  await read();await ready('asset-v2');
  const last=requests.filter(r=>r.path===endpoint).at(-1).body;
  check('updated_revision_and_source_sent',last.revision===fixture.updated_inspection.request.revision&&last.source_sha256===fixture.updated_inspection.request.source_sha256);
  await page.setViewportSize({width:390,height:844});await panel.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.screenshot({path:'.dev/w0208-hook-inspection/inspection-390.png',fullPage:true});
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));
  check('offline_clears_result',await panel.locator('.snapshot-asset').count()===0);
  check('offline_disables_read',await panel.getByRole('button').isDisabled());
  before=count();await page.evaluate(()=>window.dispatchEvent(new Event('online')));
  await panel.getByRole('button').waitFor();
  await page.waitForFunction(()=>!document.querySelector('.plugin-hook-inspection button')?.disabled);
  check('reconnect_needs_explicit_read',await panel.locator('.snapshot-asset').count()===0&&count()===before);
  await read();await ready('asset-v2');
  before=count();
  await Promise.all([page.waitForResponse(r=>r.url().endsWith('/api/capabilities')),page.getByRole('button',{name:'刷新来源与引用',exact:true}).click()]);
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  await open();
  check('source_refresh_clears_result',await panel.locator('.snapshot-asset').count()===0);
  check('source_refresh_does_not_read',count()===before);
  check('no_execution_or_mutation_requests',requests.every(r=>r.method==='GET'||r.path===endpoint));
  check('no_browser_errors',errors.length===0);
  return {checks,requests,errors,scope:'built UI with real production native metadata exports; controlled HTTP/SSE and synthetic conflict, mismatch, delay and disconnect; browser does not execute native hooks'};
}
