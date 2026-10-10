// Real native MCP inventories through Application/Handler; historical-source
// rows are explicit contract fixtures, not claims of native tool execution.
async page => {
  page.setDefaultTimeout(4000);await page.unrouteAll({behavior:'ignoreErrors'});
  const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
  const sid=fixture.sessions[0];let history=false;
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  page.__mcpOrigin={checks,requests,errors};page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};},sid);
  await page.route('**/*',async route=>{
    const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];requests.push({path,method:r.method()});
    if(fixture.responses[path]) {
      const response=JSON.parse(JSON.stringify(fixture.responses[path]));
      if(history&&response.session?.id===sid) {
        const base={server:'baseline',mode:'history',runtime_status:'unreported',verified:false,tools:[],at:'2026-10-03T12:00:00Z'};
        response.session.view.capability_evidence.push({...base,source:'plugin',plugin_id:'fixture@old',used_tools:['historic-plugin-tool'],detail:'历史来源，不表示本次加载'},
          {...base,source:'unknown',used_tools:['legacy-tool'],detail:'历史调用未报告来源，未归入当前资产'});
      }
      return route.fulfill({json:response});
    }
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'unmapped fixture'});
  });
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9464/');await page.locator('.execution-snapshot>summary').click();
  const panel=page.locator('.execution-snapshot');let text=await panel.innerText();
  check('full_native_plugin_id_visible',text.includes('原生插件 ID：fixture@owned'));
  check('non_plugin_is_not_called_user_layer',text.includes('原生来源：原生配置'));
  check('plugin_source_visible',text.includes('原生来源：CLI 插件'));
  check('selected_user_binding_visible',text.includes('绑定能力：baseline'));
  check('initialization_not_claimed_usage',!text.includes('historic-plugin-tool')&&text.includes('历史成功调用：暂无证据'));
  await panel.locator('.snapshot-asset').filter({hasText:'原生插件 ID：fixture@owned'}).scrollIntoViewIfNeeded();
  await page.screenshot({path:'.dev/w0208-codex-binding/native-1440.png',fullPage:true});
  history=true;await page.reload();await page.locator('.execution-snapshot>summary').click();
  const historical=page.getByRole('region',{name:'历史 MCP 来源'});
  check('history_separate_from_current_inventory',await historical.count()===1);
  text=await historical.innerText();
  check('historical_plugin_id_preserved',text.includes('fixture@old'));
  check('legacy_origin_not_invented',text.includes('原生来源：未知来源'));
  check('both_same_name_origins_retained',text.includes('historic-plugin-tool')&&text.includes('legacy-tool'));
  check('history_not_called_inherited_or_current',text.includes('仅历史记录')&&!text.includes('继承配置')&&!text.includes('与选择一致'));
  const selected=panel.locator('.snapshot-asset').filter({hasText:'绑定能力：baseline'});
  check('history_not_credited_to_current_asset',!(await selected.innerText()).includes('legacy-tool')&&!(await selected.innerText()).includes('historic-plugin-tool'));
  check('history_does_not_claim_empty_current_inventory',!text.includes('原生工具清单：无'));
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await historical.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.screenshot({path:'.dev/w0208-codex-binding/history-390.png',fullPage:true});
  await page.setViewportSize({width:1440,height:1100});await page.locator('.session-row').filter({hasText:'MCP origins disabled'}).click();await page.locator('.execution-snapshot>summary').click();
  text=await panel.innerText();check('disabled_configuration_still_verified',text.includes('baseline · 已禁用 · 与选择一致'));
  check('sibling_plugin_remains_visible',text.includes('fixture@owned'));
  check('switch_does_not_carry_history',!text.includes('fixture@old')&&!text.includes('legacy-tool'));
  check('only_reads',requests.every(r=>r.method==='GET'));
  check('no_browser_errors',errors.length===0);
  return {checks,requests,errors,scope:'built UI and real Application/Handler native inventories; synthetic historical-source rows, controlled transport; no model or native tool calls'};
}
