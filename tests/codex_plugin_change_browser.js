// The UI sends real commands; transport replays matching native Application
// transitions exported by TestCodexNativePluginChanges.
async page => {
  page.setDefaultTimeout(4000);await page.unrouteAll({behavior:"ignoreErrors"});
  const fixture=__FIXTURE__,assets=__ASSETS__,caseName=__CASE__;
  const scenario=fixture.cases[caseName],sid=fixture.sessions[caseName==='empty-inherit'?'empty':'a'];
  let state=scenario.states.initial;
  const requests=[],checks={},errors=[];
  const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
  const session=()=>state['/api/sessions/'+sid].session;
  const originalID=session().scope.native_id,originalSnapshot=session().execution_snapshot.id;
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(name,listener){if(name==='change')window.__netcapChange=listener;}close(){}};},sid);
  await page.route('**/*',async route=>{
    const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=r.postData()?JSON.parse(r.postData()):null;
    requests.push({path,method:r.method(),body});
    if(path.endsWith('/execution/preview')) {
      check('preview_uses_original_snapshot',body.base_snapshot_id===originalSnapshot);
      check('preview_submits_asset_mode_version',body.selections.some(s=>s.asset_id===scenario.selection.asset_id&&s.mode===scenario.selection.mode&&(s.version??'')===(scenario.selection.version??(scenario.selection.mode==='inherit'?'':'asset-v1'))));
      check('ordinary_mcp_omits_version',body.selections.filter(s=>s.asset_id==='baseline').every(s=>s.version===undefined));
      return route.fulfill({json:scenario.preview});
    }
    if(path==='/api/commands') {
      const receipt=scenario.receipts[body.kind];check('only_recorded_commands',!!receipt);
      if(body.kind!=='stage_execution')check('commands_reference_current_candidate',body.execution_change.base_snapshot_id===originalSnapshot&&body.execution_change.pending_change_id===session().pending_execution_change.id);
      if(body.kind==='stage_execution') {
        check('save_uses_preview_hash',body.execution_change.preview_hash===scenario.preview.preview_hash);state=scenario.states.staged;
        check('saved_snapshot_still_original',session().execution_snapshot.id===originalSnapshot);
      } else if(body.kind==='prepare_execution') state=scenario.states.prepared;
      else if(body.kind==='apply_execution') state=scenario.states.applied;
      await route.fulfill({status:202,json:{...receipt,command_id:body.command_id}});await page.evaluate(()=>window.__netcapChange?.());return;
    }
    if(state[path])return route.fulfill({json:state[path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':path.endsWith('.svg')?'image/svg+xml':'text/html'});
    return route.fulfill({status:404,body:'unmapped fixture path'});
  });
  await page.setViewportSize({width:1440,height:1100});await page.goto('http://127.0.0.1:9455/');
  await page.locator('.execution-snapshot>summary').click();
  const section=page.getByRole('region',{name:'原会话能力改配'});
  await section.waitFor();
  if(caseName==='add')await section.getByLabel('添加 CLI 插件 资产',{exact:true}).selectOption('plugin-1');
  else if(caseName==='upgrade') {
    check('library_update_keeps_pinned_choice',await section.locator('[data-plugin-version="plugin-1"]').inputValue()==='asset-v1');
    await section.locator('[data-plugin-version="plugin-1"]').selectOption('asset-v2');
  } else await section.locator('[data-plugin-mode="plugin-1"]').selectOption(scenario.selection.mode);
  check('plugin_mode_available',await section.locator('[data-plugin-mode="plugin-1"]').count()===1);
  await section.getByRole('button',{name:'预览改配差异',exact:true}).click();
  await section.getByText(`候选版本 ${scenario.preview.next_revision} · 与当前生效快照比较`,{exact:true}).waitFor();
  check('plugin_diff_labeled',(await section.innerText()).includes('· CLI 插件：'));
  if(caseName==='upgrade')check('explicit_version_diff',(await section.innerText()).includes('资源 asset-v1 → asset-v2'));
  await section.getByRole('button',{name:'保存待应用选择',exact:true}).click();
  await section.getByText(`待应用版本 ${scenario.preview.next_revision} · 尚未生效`,{exact:true}).waitFor();
  await section.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).click();
  await section.getByText('已取得空闲依据；明确应用将取消当前连接订阅，并按原 ID 加载新能力版本。',{exact:true}).waitFor();
  check('native_idle_allows_explicit_apply',session().view.native_status==='idle'&&await section.getByRole('button',{name:'应用已保存配置',exact:true}).isEnabled());
  check('preparation_keeps_paused',session().paused&&session().execution_hold);
  await section.getByRole('button',{name:'应用已保存配置',exact:true}).click();
  await section.getByRole('button',{name:'撤销待应用选择',exact:true}).waitFor({state:'hidden'});
  check('applied_same_id',session().scope.native_id===originalID);
  check('applied_candidate_revision',session().execution_snapshot.revision===scenario.preview.next_revision);
  check('old_version_history_visible',(await page.locator('.execution-snapshot').innerText()).includes('历史版本'));
  if(scenario.selection.mode==='inherit')check('released_plugin_can_be_added_again',await section.getByLabel('添加 CLI 插件 资产',{exact:true}).locator('option[value="plugin-1"]').count()===1);
  await section.scrollIntoViewIfNeeded();await page.screenshot({path:`.dev/w0208-codex-plugin-change/${caseName}-1440.png`,fullPage:true});
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await section.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.screenshot({path:`.dev/w0208-codex-plugin-change/${caseName}-390.png`,fullPage:true});
  check('no_browser_errors',errors.length===0);
  return {case:caseName,checks,requests,errors,scope:'built UI with matched native Application transitions; controlled HTTP/EventSource, no native work triggered by browser'};
}
