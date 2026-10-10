// The UI sends real commands; transport replays matching native Application
// transitions exported by TestClaudeNativeApplicationSkillChanges.
async page => {
  const fixture=__FIXTURE__,assets=__ASSETS__,caseName=__CASE__;
  const scenario=fixture.cases[caseName],sid=fixture.sessions.a;
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
      check('preview_submits_asset_mode_version',body.selections.length===1&&body.selections[0].asset_id===scenario.selection.asset_id&&body.selections[0].mode===scenario.selection.mode&&(body.selections[0].version??'')===(scenario.selection.version??(caseName==='inherit'?'':'v1')));
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
  if(caseName==='add')await section.getByLabel('添加 Skill 资产',{exact:true}).selectOption('shared');
  else if(caseName==='upgrade') {
    check('library_update_keeps_pinned_choice',await section.locator('[data-skill-version="shared"]').inputValue()==='v1');
    await section.locator('[data-skill-version="shared"]').selectOption('v2');
  } else await section.locator('[data-skill-mode="shared"]').selectOption('inherit');
  check('skill_mode_available',await section.locator('[data-skill-mode="shared"]').count()===1);
  await section.getByRole('button',{name:'预览改配差异',exact:true}).click();
  await section.getByText(`候选版本 ${scenario.preview.next_revision} · 与当前生效快照比较`,{exact:true}).waitFor();
  check('skill_diff_labeled',(await section.innerText()).includes('· Skill：'));
  if(caseName==='upgrade')check('explicit_version_diff',(await section.innerText()).includes('资源 v1 → v2'));
  await section.getByRole('button',{name:'保存待应用选择',exact:true}).click();
  await section.getByText(`待应用版本 ${scenario.preview.next_revision} · 尚未生效`,{exact:true}).waitFor();
  await section.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).click();
  await section.getByText('已取得空闲依据；明确应用将关闭此查询并按原 ID 加载新版本。',{exact:true}).waitFor();
  check('preparation_keeps_paused',session().paused&&session().execution_hold);
  await section.getByRole('button',{name:'应用已保存配置',exact:true}).click();
  await section.getByRole('button',{name:'撤销待应用选择',exact:true}).waitFor({state:'hidden'});
  check('applied_same_id',session().scope.native_id===originalID);
  check('applied_candidate_revision',session().execution_snapshot.revision===scenario.preview.next_revision);
  check('old_version_history_visible',(await page.locator('.execution-snapshot').innerText()).includes('历史版本'));
  if(caseName==='inherit')check('released_skill_can_be_added_again',await section.getByLabel('添加 Skill 资产',{exact:true}).locator('option[value="shared"]').count()===1);
  await section.scrollIntoViewIfNeeded();await page.screenshot({path:`.dev/w0207-change/${caseName}-1440.png`,fullPage:true});
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await section.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.screenshot({path:`.dev/w0207-change/${caseName}-390.png`,fullPage:true});
  check('no_browser_errors',errors.length===0);
  return {case:caseName,checks,requests,errors,scope:'built UI with matched native Application transitions; controlled HTTP/EventSource, no native work triggered by browser'};
}
