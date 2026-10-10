// Phase one: prepare a native thread. Then restart only the owned fixture daemon.
async (page) => {
  await page.setViewportSize({width:1440,height:1000});
  await page.getByRole('button',{name:'＋ 新建',exact:true}).click();
  const title=`W02.04 原 ID 应用验收 ${Date.now()}`;
  await page.getByLabel('会话名称',{exact:true}).fill(title);
  await page.locator('select[name="capability_selection:identity"]').selectOption('enabled');
  const created=page.waitForResponse(r=>r.url().endsWith('/api/commands') && r.request().postDataJSON()?.kind==='create');
  await page.getByRole('button',{name:'创建会话',exact:true}).click();
  const sid=(await (await created).json()).session_id;
  await page.getByRole('heading',{name:title,exact:true}).waitFor();
  await page.getByRole('textbox',{name:'消息',exact:true}).fill('NETCAP_A_TURN_0');
  await page.getByRole('button',{name:'发送消息',exact:true}).click();
  await page.getByText('fixture A: auxiliary',{exact:true}).waitFor();
  await page.locator('.execution-snapshot > summary').click();
  await page.getByRole('combobox',{name:'identity · 待应用选择',exact:true}).selectOption('disabled');
  await page.getByRole('button',{name:'预览改配差异',exact:true}).click();
  await page.getByRole('button',{name:'保存待应用选择',exact:true}).click();
  await page.getByText('待应用版本 2 · 尚未生效',{exact:true}).waitFor();
  await page.getByRole('button',{name:'准备应用（保持暂停）',exact:true}).click();
  await page.getByText('已取得空闲依据，等待原线程卸载；当前版本不能热切换。',{exact:true}).waitFor();
  const checks={prepare_visible:true,loaded_apply_disabled:await page.getByRole('button',{name:'应用已保存配置',exact:true}).isDisabled(),held_send_disabled:await page.getByRole('button',{name:'发送消息',exact:true}).isDisabled()};
  await page.evaluate(({sid,checks})=>sessionStorage.setItem('w0204-browser',JSON.stringify({sid,checks})),{sid,checks});
  if(!Object.values(checks).every(Boolean))throw Error(JSON.stringify(checks));
  return {session_id:sid,checks};
}
