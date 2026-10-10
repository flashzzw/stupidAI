// Native Application/Handler exports; controlled browser transport only.
async page => {
  page.setDefaultTimeout(4000);await page.unrouteAll({behavior:'ignoreErrors'});
  const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
  let mode='handler';const sid=fixture.sessions[0];
  const check=(key,value)=>{checks[key]=!!value;if(!value)throw Error(key);};
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};},sid);
  await page.route('**/*',async route=>{
    const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=r.postData()?JSON.parse(r.postData()):null;requests.push({path,method:r.method(),body});
    if(path.endsWith('/hooks/catalog')){
      check('catalog_body_empty',r.method()==='POST'&&JSON.stringify(body)==='{}');
      if(mode==='failure')return route.fulfill({status:409,json:{error:'原生 hook 来源查询失败；未取得本次清单'}});
      return route.fulfill({json:fixture.inventories[mode]});
    }
    if(fixture.responses[path])return route.fulfill({json:fixture.responses[path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'unmapped fixture route'});
  });
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9462/');
  await page.locator('.execution-snapshot>summary').click();
  const panel=page.locator('.hook-catalog');await panel.getByText('Codex hook 来源清单',{exact:true}).click();
  check('no_automatic_query',!requests.some(r=>r.path.endsWith('/hooks/catalog')));
  await panel.getByRole('button',{name:'读取当前 hooks',exact:true}).click();
  await panel.getByText('按工作目录发现 · 3 项 hook · 覆盖不完整',{exact:true}).waitFor();
  let text=await panel.innerText();
  check('scope_is_directory',text.includes('不代表本会话已加载')&&text.includes('工作目录：'));
  check('all_native_sources_visible',['来源：用户','来源：项目','来源：CLI 插件 · fixture@owned'].every(s=>text.includes(s)));
  check('native_cache_path_visible',text.includes('/plugins/cache/owned/fixture/1.0.0/hooks/hooks.json'));
  check('untrusted_not_success',text.includes('信任状态：未信任 · 不代表执行成功'));
  check('no_command_body',!text.includes('PRIVATE_')&&!text.includes('printf'));
  await panel.locator('summary').filter({hasText:'定义摘要'}).first().click();
  check('native_hash_prefix_preserved',(await panel.innerText()).includes('sha256:'));
  await page.screenshot({path:'.dev/w0208-codex-hooks/catalog-1440.png',fullPage:true});
  mode='broken';await panel.getByRole('button',{name:'读取当前 hooks',exact:true}).click();
  await panel.getByText('按工作目录发现 · 2 项 hook · 覆盖不完整',{exact:true}).waitFor();text=await panel.innerText();
  check('native_warning_count_visible',text.includes('原生报告 1 条提示'));
  check('filtered_source_not_displayed',!text.includes('来源：项目')&&text.includes('fixture@owned'));
  mode='repaired';await panel.getByRole('button',{name:'读取当前 hooks',exact:true}).click();await panel.getByText('按工作目录发现 · 3 项 hook · 覆盖不完整',{exact:true}).waitFor();
  check('repair_replaces_warning',!(await panel.innerText()).includes('原生报告 1 条提示'));
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await panel.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.screenshot({path:'.dev/w0208-codex-hooks/catalog-390.png',fullPage:true});
  mode='failure';await panel.getByRole('button',{name:'读取当前 hooks',exact:true}).click();await panel.getByRole('alert').waitFor();
  check('failure_clears_previous_rows',!(await panel.innerText()).includes('fixture@owned'));
  mode='handler';await panel.getByRole('button',{name:'读取当前 hooks',exact:true}).click();await panel.getByText('按工作目录发现 · 3 项 hook · 覆盖不完整',{exact:true}).waitFor();
  await page.setViewportSize({width:1440,height:1100});
  const queriesBeforeSwitch=requests.filter(r=>r.path.endsWith('/hooks/catalog')).length;
  await page.locator('.session-row').filter({hasText:'Hook discovery 2'}).click();
  await page.locator('.execution-snapshot>summary').click();await panel.locator('summary').first().click();
  check('session_switch_clears_rows',await panel.locator('.snapshot-asset').count()===0);
  check('session_switch_does_not_query',requests.filter(r=>r.path.endsWith('/hooks/catalog')).length===queriesBeforeSwitch);
  await page.locator('.session-row').filter({hasText:'Hook discovery 1'}).click();
  await page.locator('.execution-snapshot>summary').click();await panel.locator('summary').first().click();
  await panel.getByRole('button',{name:'读取当前 hooks',exact:true}).click();await panel.getByText('按工作目录发现 · 3 项 hook · 覆盖不完整',{exact:true}).waitFor();
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));
  check('offline_clears_sample',await page.locator('.hook-catalog .snapshot-asset').count()===0);
  check('offline_disables_read',await page.locator('.hook-catalog button').isDisabled());
  check('no_mutation_routes',requests.every(r=>r.method==='GET'||r.path.endsWith('/hooks/catalog')));
  check('no_browser_errors',errors.length===0);
  return {checks,requests,errors,scope:'built UI replay of native directory catalogs and actual Application/Handler session data; controlled HTTP/SSE, failure transport synthetic; no native execution from browser'};
}
