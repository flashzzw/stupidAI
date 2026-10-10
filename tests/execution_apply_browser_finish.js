// Phase two: the owned fixture has restarted its daemon. No native calls from JS.
async (page) => {
  const {sid,checks}=await page.evaluate(()=>JSON.parse(sessionStorage.getItem('w0204-browser')));
  const check=(name,value)=>{checks[name]=Boolean(value);if(!value)throw Error(name);};
  await page.getByText('原线程已卸载，可明确应用新版本。',{exact:true}).waitFor();
  check('unloaded_apply_enabled',await page.getByRole('button',{name:'应用已保存配置',exact:true}).isEnabled());
  const before=await page.evaluate(async sid=>(await fetch(`/api/sessions/${sid}/execution`)).json(),sid);
  await page.getByRole('button',{name:'应用已保存配置',exact:true}).click();
  await page.getByText('identity · 已禁用 · 与选择一致',{exact:true}).waitFor();
  await page.getByText('历史执行快照 · 1 项',{exact:true}).waitFor();
  const after=await page.evaluate(async sid=>(await fetch(`/api/sessions/${sid}/execution`)).json(),sid);
  check('candidate_promoted',JSON.stringify(before.pending_change.snapshot)===JSON.stringify(after.snapshot));
  check('history_unchanged',JSON.stringify(after.history)==JSON.stringify([before.snapshot]));
  check('hold_cleared',!after.hold && !after.pending_change);
  await page.getByText('历史版本 1',{exact:true}).click();
  check('old_mcp_selection_visible',await page.getByText('identity · 启用',{exact:true}).count()===1);
  await page.getByRole('textbox',{name:'消息',exact:true}).fill('NETCAP_A_TURN_1');
  await page.getByRole('button',{name:'发送消息',exact:true}).click();
  await page.getByText('fixture A: NETCAP_A_TURN_1',{exact:true}).waitFor();
  check('no_approval_for_disabled_tool',await page.getByRole('button',{name:'允许本次',exact:true}).count()===0);
  await page.reload();
  await page.locator('.execution-snapshot > summary').click();
  await page.getByText('历史执行快照 · 1 项',{exact:true}).waitFor();
  const restored=await page.evaluate(async sid=>(await fetch(`/api/sessions/${sid}/execution`)).json(),sid);
  check('reload_keeps_version_history',JSON.stringify(after.snapshot)===JSON.stringify(restored.snapshot) && JSON.stringify(after.history)===JSON.stringify(restored.history));
  for(const width of [1440,768]){
    await page.setViewportSize({width,height:1000});
    if(width===768)await page.getByRole('button',{name:'详情',exact:true}).click();
    await page.getByText('历史执行快照 · 1 项',{exact:true}).scrollIntoViewIfNeeded();
    check(`layout_${width}`,await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
    await page.screenshot({path:`/home/zzw/codex-netcap/.dev/w02-apply/browser-${width}.png`});
  }
  return {scope:'production browser; explicit same-ID apply after owned daemon restart',session_id:sid,checks,expectations_met:Object.values(checks).every(Boolean)};
}
