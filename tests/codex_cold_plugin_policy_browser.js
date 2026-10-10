// Real Handler snapshots; browser commands and SSE are replayed, not sent native.
async page => {
  const fixture=__FIXTURE__,assets=__ASSETS__,start=__PHASE__,checks={},requests=[],errors=[];
  const source=fixture.samples.retire_staged,sid=source.id;
  let phase=start,sequence=1000;
  const check=(name,value)=>{checks[name]=!!value;if(!value)throw Error(name);};
  page.setDefaultTimeout(5000);await page.unrouteAll({behavior:'ignoreErrors'});
  page.on('pageerror',e=>errors.push(e.message));
  await page.addInitScript(id=>{sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{
    constructor(){this.listeners={};window.coldPolicyEvents=this;setTimeout(()=>this.onopen?.(),0);}addEventListener(name,fn){this.listeners[name]=fn;}close(){}
  };},sid);
  await page.route('**/*',async route=>{
    const req=route.request(),path=req.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0],body=req.postData()?JSON.parse(req.postData()):undefined;
    requests.push({path,method:req.method(),body});
    if(path==='/api/commands'){
      const next={prepare_execution:'retire_prepared',apply_execution:'retired'}[body.kind];
      if(!next)return route.fulfill({status:409,json:{error:'unexpected fixture command'}});
      phase=next;sequence+=100;
      await route.fulfill({json:{command_id:body.command_id,session_id:sid,status:'accepted'}});
      await page.evaluate(()=>window.coldPolicyEvents.listeners.change?.());return;
    }
    if(fixture.responses[phase]?.[path]){const row=JSON.parse(JSON.stringify(fixture.responses[phase][path]));row.sequence=sequence;return route.fulfill({json:row});}
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    if(path==='/favicon.ico')return route.fulfill({status:204,body:''});
    return route.fulfill({status:404,body:'unmapped fixture path'});
  });
  await page.setViewportSize({width:1440,height:1000});await page.goto('http://127.0.0.1:9463/');
  const snapshot=page.locator('.execution-snapshot');await snapshot.locator(':scope > summary').click();
  const panel=page.getByRole('region',{name:'原会话配置改配',exact:true});
  await panel.getByText(/已保存原配置依据/).waitFor();
  const initial=fixture.samples[start],saved=initial.pending_execution_change;
  check('old_snapshot_still_visible',(await snapshot.innerText()).includes(initial.execution_snapshot.id));
  check('original_mixed_assets_recorded',initial.scope.plugin_selections.length===2);
  check('pending_removes_both_assets',!saved.snapshot.plugin_selections?.length);
  check('proof_explains_recheck',(await panel.innerText()).includes('保存时的受管策略'));
  check('ordinary_input_disabled',await page.getByRole('button',{name:'发送消息',exact:true}).isDisabled());
  const diff=panel.getByRole('region',{name:'待采纳 hook 文件',exact:true});
  const diffText=await diff.innerText();check('both_file_hashes_visible',diffText.includes(saved.codex_hook_source_changes[0].before.sha256)&&diffText.includes(saved.codex_hook_source_changes[0].after.sha256));
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await panel.scrollIntoViewIfNeeded();
  check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await page.screenshot({path:'.dev/w0209-codex-cold-mixed/'+start+'-390.png',fullPage:true});
  const unknown=start==='cold_loss_reopened';
  if(unknown){
    check('unknown_not_promoted',saved.status==='applying'&&initial.execution_snapshot.id===source.execution_snapshot.id);
    check('no_apply_button',await panel.getByRole('button',{name:'应用已保存配置',exact:true}).count()===0);
    check('no_prepare_button',await panel.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).count()===0);
    check('unknown_explained',(await panel.innerText()).includes('实际生效待核对'));
  }else{
    if(start==='retire_cold_pending'){
      check('prepare_available',await panel.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).isEnabled());
      await panel.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).click();
    }else check('saved_baseline_retained',!!saved.baseline&&initial.execution_hold===true);
    const apply=panel.getByRole('button',{name:'应用已保存配置',exact:true});await apply.waitFor();check('explicit_apply_available',await apply.isEnabled());
    await apply.click();await snapshot.getByText(fixture.samples.retired.execution_snapshot.id,{exact:true}).waitFor();
    check('retired_original_id',fixture.samples.retired.scope.native_id===initial.scope.native_id);
    check('promoted_exact_candidate',fixture.samples.retired.execution_snapshot.id===saved.snapshot.id);
    check('pending_cleared',!(await panel.innerText()).includes('待应用版本 3'));
  }
  const commands=requests.filter(r=>r.path==='/api/commands');
  check('only_explicit_commands',commands.map(r=>r.body.kind).join(',')===(unknown?'':start==='retire_cold_pending'?'prepare_execution,apply_execution':'apply_execution'));
  check('only_saved_candidate_reference',commands.every(r=>r.body.execution_change.pending_change_id===saved.id&&r.body.execution_change.base_snapshot_id===initial.execution_snapshot.id&&!r.body.execution_change.selections&&!r.body.execution_change.adopt_codex_hook_sources));
  check('no_page_errors',errors.length===0);
  return {phase:start,checks,passed:Object.values(checks).every(Boolean),requests,errors,scope:'actual cold mixed Handler responses; command and SSE replay only'};
}
