// Insert only owned fixture assets at ASSETS; use a separate authenticated session.
async page => {
  const assets = /*ASSETS*/{};
  const checks = {}, errors = [];
  const onError = error => errors.push(String(error));
  const onConsole = message => { if (message.type() === 'error') errors.push(message.text()); };
  page.on('pageerror', onError); page.on('console', onConsole);
  const check = (name, ok) => { checks[name] = Boolean(ok); if (!ok) throw Error(name); };
  const library = () => page.evaluate(async () => (await fetch('/api/capabilities')).json());
  const readSession = id => page.evaluate(async id => (await (await fetch(`/api/sessions/${id}`)).json()).session, id);
  try {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.getByRole('button', { name: '能力库', exact: true }).click();
    await page.getByRole('heading', { name: '能力库', exact: true }).waitFor();
    await page.getByText('此分区还没有能力资产。已有目录默认登记为外部管理。', { exact: true }).waitFor();
    await page.getByRole('button', { name: '登记或导入能力', exact: true }).click();
    const dialog = page.getByRole('dialog');
    for (const [field, value] of Object.entries({ id: assets.v1.id, name: assets.v1.name, version: 'v1', source: assets.v1.source })) await dialog.locator(`[name="${field}"]`).fill(value);
    await dialog.getByRole('combobox', { name: '管理方式', exact: true }).selectOption('managed');
    await dialog.getByRole('button', { name: '预览影响', exact: true }).click();
    await dialog.getByRole('region', { name: '能力变更预览' }).waitFor();
    check('preview_does_not_create_library_entry', (await library()).length === 0);
    check('managed_copy_boundary_visible', await dialog.getByText(/将所选资源复制为控制台管理的固定版本/).count() === 1);
    await dialog.getByRole('button', { name: '确认保存登记', exact: true }).click();
    await dialog.waitFor({ state: 'hidden' });
    await page.getByText('平台管理 · 固定版本', { exact: true }).waitFor();
    const v1 = (await library())[0];
    check('saved_managed_source_is_separate', v1.asset.source !== assets.v1.source && v1.versions.length === 1);
    await page.getByRole('button', { name: '＋ 新建', exact: true }).click();
    const create = page.getByRole('dialog');
    await create.getByLabel('会话名称', { exact: true }).fill('W02.05 固定版本引用');
    await create.locator('select[name="route"]').selectOption('claude-b-route');
    await create.locator('input[name="capability_ref"][value="shared-method"]').check();
    const created = page.waitForResponse(r => r.url().endsWith('/api/commands') && r.request().postDataJSON()?.kind === 'create');
    await create.getByRole('button', { name: '创建会话', exact: true }).click();
    const sid = (await (await created).json()).session_id;
    await page.getByRole('heading', { name: 'W02.05 固定版本引用', exact: true }).waitFor();
    await page.waitForFunction(async id => (await (await fetch(`/api/sessions/${id}`)).json()).session.status === 'idle', sid);
    const before = (await readSession(sid)).execution_snapshot;
    check('new_session_records_managed_version', before.capability_refs[0].asset.version === 'v1' && before.capability_refs[0].asset.management === 'managed');
    await page.getByLabel('消息', { exact: true }).fill('NETCAP_B_TURN_1');
    await page.getByRole('button', { name: '发送消息', exact: false }).click();
    await page.getByText('fixture B: NETCAP_B_TURN_1', { exact: true }).waitFor();
    await page.getByRole('button', { name: '能力库', exact: true }).click();
    await page.getByRole('button', { name: '更新登记版本', exact: true }).waitFor();
    await page.getByRole('button', { name: '更新登记版本', exact: true }).click();
    await dialog.getByLabel('新版本标识', { exact: true }).fill('v2');
    await dialog.getByLabel('资源来源', { exact: true }).fill(assets.v2.source);
    await dialog.getByRole('button', { name: '预览影响', exact: true }).click();
    await dialog.getByRole('region', { name: '能力变更预览' }).waitFor();
    check('update_previews_actual_session_use', (await dialog.locator('.capability-uses').textContent()).includes('W02.05 固定版本引用 · v1 · 当前版本'));
    await page.setViewportSize({ width: 768, height: 1000 });
    await dialog.getByRole('region', { name: '能力变更预览' }).scrollIntoViewIfNeeded();
    check('preview_768_no_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
    await page.screenshot({ path: '/home/zzw/codex-netcap/.dev/w0205/preview-768.png' });
    await dialog.getByRole('button', { name: '确认保存登记', exact: true }).click();
    await dialog.waitFor({ state: 'hidden' });
    await page.waitForFunction(async () => (await (await fetch('/api/capabilities')).json())[0].asset.version === 'v2');
    await page.locator('.capability-card .muted').filter({ hasText: '· v2 ·' }).waitFor();
    const v2 = (await library())[0];
    check('update_preserves_v1_resource_and_snapshot', v2.versions.length === 2 && v2.versions[0].asset.source === v1.asset.source && JSON.stringify((await readSession(sid)).execution_snapshot) === JSON.stringify(before));
    for (const action of ['停用新引用', '移除登记']) {
      await page.getByRole('button', { name: action, exact: true }).click();
      await dialog.getByRole('button', { name: '预览影响', exact: true }).click();
      await dialog.getByRole('region', { name: '能力变更预览' }).waitFor();
      await dialog.getByRole('button', { name: '确认保存登记', exact: true }).click();
      await dialog.waitFor({ state: 'hidden' });
      if (action === '停用新引用') await page.getByText('已停用新引用', { exact: true }).waitFor();
      else await page.locator('.capability-card .badge').filter({ hasText: '已移除登记' }).waitFor();
    }
    check('remove_retains_session_snapshot', JSON.stringify((await readSession(sid)).execution_snapshot) === JSON.stringify(before));
    await page.locator('.library-history > summary').click();
    await page.getByRole('button', { name: 'W02.05 固定版本引用', exact: true }).waitFor();
    for (const width of [1440, 768]) {
      await page.setViewportSize({ width, height: 1000 });
      check(`library_layout_${width}`, await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      await page.screenshot({ path: `/home/zzw/codex-netcap/.dev/w0205/library-${width}.png` });
    }
    await page.getByRole('button', { name: 'W02.05 固定版本引用', exact: true }).click();
    await page.getByRole('heading', { name: 'W02.05 固定版本引用', exact: true }).waitFor();
    await page.reload();
    await page.getByRole('heading', { name: 'W02.05 固定版本引用', exact: true }).waitFor();
    check('reload_keeps_selected_original_session', (await readSession(sid)).scope.native_id !== '');
    check('no_browser_errors', errors.length === 0);
    return { checks, errors, session_id: sid, expectations_met: Object.values(checks).every(Boolean) };
  } finally { page.off('pageerror', onError); page.off('console', onConsole); }
}
