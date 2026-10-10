// The fixture is the real native test's Handler output; compatibility cases
// below explicitly transform only API fields that old/unknown servers omit.
async page => {
  const fixture=__FIXTURE__, assets=__ASSETS__, checks={}, requests=[], errors=[];
  const initial=fixture.responses.initial;
  const executionPath=Object.keys(initial).find(path=>path.endsWith('/execution'));
  const sid=initial[executionPath].snapshot.session_id;
  let phase='initial', variant='real';
  const check=(name,value)=>{checks[name]=!!value;if(!value)throw Error(name);};
  page.setDefaultTimeout(6000);
  await page.unrouteAll({behavior:'ignoreErrors'});
  await page.addInitScript(id=>{
    sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);
    window.EventSource=class { constructor(){window.ownedEventSource=this;setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){} };
  },sid);
  page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/*',async route=>{
    const req=route.request(),path=req.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];
    requests.push({path,method:req.method()});
    if(fixture.responses[phase]?.[path]) {
      const data=JSON.parse(JSON.stringify(fixture.responses[phase][path]));
      if(path===executionPath){
        if(variant==='legacy'){
          delete data.snapshot.codex_user_hooks;delete data.snapshot.codex_project_hooks;
          data.sources=data.sources.filter(s=>!s.group.startsWith('codex_'));
          for(const area of data.source_coverage.areas)if(area.id.startsWith('codex_')){area.status='unrecorded';area.detail='此快照未记录该类来源；本次检查不能判断它是否变化，也不会补造历史版本。';}
        }
        if(variant==='older-api') {delete data.source_coverage;for(const source of data.sources)delete source.group;delete data.native_evidence;}
        if(variant==='unknown') {data.status='future-status';data.source_coverage.scope='future-scope';}
        if(variant==='replaced') data.snapshot.id='new-snapshot';
      }
      return route.fulfill({json:data});
    }
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    if(path==='/favicon.ico')return route.fulfill({status:204,body:''});
    return route.fulfill({status:409,body:'No mutation permitted in this replay'});
  });
  await page.setViewportSize({width:1440,height:1000});
  await page.goto('http://127.0.0.1:9463/');
  const snapshot=page.locator('.execution-snapshot');await snapshot.locator(':scope > summary').click();
  const source=page.locator('.execution-source-check');
  const button=source.getByRole('button',{name:'核对当前来源',exact:true});
  check('no_automatic_file_check',requests.every(r=>r.path!==executionPath));
  await button.click();
  await source.getByText('已记录的来源与绑定未发现变化。',{exact:true}).waitFor();
  const coverage=source.getByRole('region',{name:'本次来源检查覆盖',exact:true});
  check('recorded_user_source_visible',await coverage.getByText('Codex 用户 hook 文件 · 已记录',{exact:true}).isVisible());
  check('empty_project_collection_is_recorded',await coverage.getByText('Codex 项目 hook 文件 · 已记录',{exact:true}).isVisible());
  check('unselected_assets_are_not_absent_claim',await coverage.getByText('显式能力引用 · 未显式选择',{exact:true}).isVisible());
  check('four_gaps_visible',await coverage.locator('summary').filter({hasText:'尚未覆盖'}).count()===4);
  await coverage.getByText('CLI 主配置文件 · 已记录',{exact:true}).click();
  check('unchanged_file_path_visible',await coverage.getByText(initial[executionPath].snapshot.profile_source.path,{exact:true}).isVisible());
  const native=source.getByRole('region',{name:'最近保存的原生观察',exact:true});
  check('old_observation_source_visible',(await native.innerText()).includes(initial[executionPath].native_evidence.source));
  check('file_check_not_native_refresh',(await native.innerText()).includes('本次文件检查不会刷新原生观察'));
  check('observation_not_full_config',(await native.innerText()).includes('不代表完整有效配置'));
  const oldObservation=await native.innerText();
  phase='drift';await button.click();
  await source.getByText('来源或绑定已变化，继续前需核对。',{exact:true}).waitFor();
  const hooks=coverage.locator('details').filter({has:page.getByText('Codex 用户 hook 文件 · 已记录',{exact:true})});
  check('drift_group_opens_automatically',await hooks.getAttribute('open')!==null);
  check('drift_path_visible',(await hooks.innerText()).includes(initial[executionPath].snapshot.codex_user_hooks.file.path));
  check('drift_does_not_replace_observation',await native.innerText()===oldObservation);
  await source.scrollIntoViewIfNeeded();await page.screenshot({path:'.dev/w0209-source-coverage/drift-1440.png',fullPage:true});
  phase='restored';await button.click();
  await source.getByText('已记录的来源与绑定未发现变化。',{exact:true}).waitFor();
  check('restored_gaps_remain',await coverage.locator('summary').filter({hasText:'尚未覆盖'}).count()===4);
  variant='legacy';await button.click();
  await coverage.getByText('Codex 用户 hook 文件 · 快照未记录',{exact:true}).waitFor();
  check('legacy_project_not_invented',await coverage.getByText('Codex 项目 hook 文件 · 快照未记录',{exact:true}).isVisible());
  check('legacy_overall_file_status_scoped',await source.getByText('已记录的来源与绑定未发现变化。',{exact:true}).isVisible());
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await source.scrollIntoViewIfNeeded();
  check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await page.screenshot({path:'.dev/w0209-source-coverage/legacy-390.png',fullPage:true});
  variant='older-api';await button.click();
  await coverage.getByText('此响应未提供可识别的覆盖范围；以下仅列出返回的来源检查。',{exact:true}).waitFor();
  check('older_api_all_sources_visible',await coverage.getByText(initial[executionPath].snapshot.profile_source.path,{exact:true}).isVisible());
  check('missing_observation_not_invented',await native.getByText('尚无带时间的原生观察。',{exact:true}).isVisible());
  variant='unknown';await button.click();
  await source.getByText('来源检查尚未确认，不能判断是否变化。',{exact:true}).waitFor();
  check('unknown_not_unchanged',await source.getByText('已记录的来源与绑定未发现变化。',{exact:true}).count()===0);
  variant='replaced';await button.click();
  await source.getByRole('alert').waitFor();
  check('changed_snapshot_response_rejected',(await source.getByRole('alert').innerText()).includes('执行版本已变化'));
  check('old_result_cleared_after_error',await coverage.count()===0);
  await page.evaluate(()=>window.ownedEventSource.onerror());
  await button.waitFor();
  check('disconnect_disables_check',await button.isDisabled());
  check('no_mutations',requests.every(r=>r.method==='GET'));
  check('no_page_errors',errors.length===0);
  return {checks,passed:Object.values(checks).every(Boolean),requests,errors,scope:'real native Handler initial/drift/restored; explicit transformed legacy/unknown/version-conflict API fixtures; desktop/mobile/disconnect'};
}
