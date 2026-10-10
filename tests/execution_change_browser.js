// Run in an authenticated, owned native_mcp_selection_smoke --hold fixture.
async (page) => {
  const checks = {}, errors = [];
  const onError = error => errors.push(String(error));
  page.on('pageerror', onError);
  const check = (name, ok) => { checks[name] = Boolean(ok); if (!ok) throw Error(name); };
  const execution = id => page.evaluate(async id => {
    const response = await fetch(`/api/sessions/${id}/execution`);
    if (!response.ok) throw Error(`execution read: ${response.status}`);
    return response.json();
  }, id);
  try {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.getByRole('button', { name: '＋ 新建', exact: true }).click();
    await page.getByLabel('会话名称', { exact: true }).fill('W02.04 待应用验收');
    await page.locator('select[name="capability_selection:identity"]').selectOption('enabled');
    const created = page.waitForResponse(r => r.url().endsWith('/api/commands') && r.request().postDataJSON()?.kind === 'create');
    await page.getByRole('button', { name: '创建会话', exact: true }).click();
    const sid = (await (await created).json()).session_id;
    await page.locator('.execution-snapshot summary').click();
    await page.getByText('identity · 已连接 · 与选择一致', { exact: true }).waitFor();
    const before = await execution(sid);
    await page.getByRole('combobox', { name: 'identity · 待应用选择', exact: true }).selectOption('disabled');
    await page.getByRole('button', { name: '预览改配差异', exact: true }).click();
    await page.getByText('候选版本 2 · 与当前生效快照比较', { exact: true }).waitFor();
    check('preview_does_not_save', !(await execution(sid)).pending_change);
    check('exact_difference_visible', await page.getByText('identity：启用 → 禁用', { exact: true }).count() === 1);
    check('restart_boundary_visible', await page.getByText(/当前版本的已加载 Codex 会话不能通过 resume 热切换 MCP/).count() === 1);
    await page.getByRole('button', { name: '保存待应用选择', exact: true }).click();
    await page.getByText('待应用版本 2 · 尚未生效', { exact: true }).waitFor();
    const saved = await execution(sid);
    check('save_has_linked_candidate', saved.pending_change.snapshot.previous_snapshot_id === before.snapshot.id);
    check('active_snapshot_unchanged', JSON.stringify(saved.snapshot) === JSON.stringify(before.snapshot));
    check('native_still_enabled', saved.capability_evidence.some(e => e.server === 'identity' && e.runtime_status === 'connected' && e.verified));
    check('save_receipt_is_truthful', await page.getByText('待应用选择已保存，当前生效配置不变', { exact: true }).count() === 1);
    await page.reload();
    await page.locator('.execution-snapshot summary').click();
    await page.getByText('待应用版本 2 · 尚未生效', { exact: true }).waitFor();
    const restored = await execution(sid);
    check('reload_preserves_candidate', restored.pending_change.id === saved.pending_change.id);
    for (const width of [1440, 768]) {
      await page.setViewportSize({ width, height: 1000 });
      if (width === 768) await page.getByRole('button', { name: '详情', exact: true }).click();
      await page.getByText('待应用版本 2 · 尚未生效', { exact: true }).scrollIntoViewIfNeeded();
      check(`layout_${width}`, await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      await page.screenshot({ path: `/home/zzw/codex-netcap/.dev/w02-reconfigure/browser-${width}.png` });
    }
    await page.getByRole('button', { name: '撤销待应用选择', exact: true }).click();
    await page.getByText('待应用版本 2 · 尚未生效', { exact: true }).waitFor({ state: 'hidden' });
    const discarded = await execution(sid);
    check('discard_keeps_effective_snapshot', !discarded.pending_change && JSON.stringify(discarded.snapshot) === JSON.stringify(before.snapshot));
    check('no_javascript_errors', errors.length === 0);
    return { scope: 'browser; real Codex with local provider; pending change only', checks, session_id: sid, errors, expectations_met: Object.values(checks).every(Boolean) };
  } finally { page.off('pageerror', onError); }
}
