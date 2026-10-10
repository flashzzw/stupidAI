// Replay actual Handler exports for empty-hook retirement receipt boundaries.
async page => {
  const fixture = __FIXTURE__, mode = __MODE__, assets = __ASSETS__;
  const checks = {}, requests = [], errors = [];
  const check = (name, ok) => { checks[name] = Boolean(ok); if (!ok) throw new Error(name); };
  const session = fixture.responses['/api/sessions/' + fixture.sessions.direct].session;
  const sample = fixture.samples[mode === 'application' ? 'direct-inherit-applied' : 'incomplete-retirement-state'];
  const operation = sample.operations.find(o => o.command_id === 'direct-inherit-apply');
  await page.unrouteAll({ behavior: 'ignoreErrors' });
  await page.addInitScript(id => {
    sessionStorage.clear(); sessionStorage.setItem('netcap.selected.v1', id);
    window.EventSource = class {
      constructor() { this.listeners = {}; window.retirementEvents = this; setTimeout(() => this.onopen?.(), 0); }
      addEventListener(name, fn) { this.listeners[name] = fn; }
      close() {}
    };
  }, session.id);
  page.on('pageerror', error => errors.push(error.message));
  await page.route('**/*', async route => {
    const request = route.request(), path = request.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    const body = request.postData() ? JSON.parse(request.postData()) : null;
    requests.push({ path, method: request.method(), body });
    if (path === '/api/commands') {
      if (body.kind !== 'reconcile' || body.session_id !== session.id) throw new Error('unexpected command');
      return route.fulfill({ json: { session_id: session.id, status: 'accepted' } });
    }
    if (fixture.responses[path]) return route.fulfill({ json: fixture.responses[path] });
    if (assets[path]) return route.fulfill({ path: assets[path], contentType: path.endsWith('.js') ? 'application/javascript' : path.endsWith('.css') ? 'text/css' : 'text/html' });
    return route.fulfill({ status: 404, json: { error: 'unmapped fixture route' } });
  });
  await page.setViewportSize({ width: 1440, height: 1100 });
  await page.goto('http://127.0.0.1:9470/');
  await page.getByRole('heading', { name: 'Direct retire hook', exact: true }).waitFor();
  const execution = page.locator('.execution-snapshot');
  await execution.locator(':scope > summary').click();
  const change = page.locator('.execution-change');
  const plugin = page.getByRole('region', { name: '本次 CLI 插件选择' });
  const op = page.locator('.operation').filter({ hasText: operation.id });
  await op.locator('summary').click();
  check('original_native_id_visible', (await page.locator('.inspector').innerText()).includes(session.scope.native_id));
  check('automation_remains_paused', (await page.locator('.conversation-header').innerText()).includes('自动化已暂停'));
  check('no_other_skill_added', await page.getByRole('region', { name: '本次原生 Skill 选择' }).count() === 0);
  if (mode === 'application') {
    check('acknowledged_retirement_visible', await plugin.count() === 0 && (await op.locator('summary').innerText()).includes('已完成'));
    check('acknowledged_has_no_pending_change', !session.pending_execution_change && await change.getByText(/实际生效待核对/).count() === 0);
    check('acknowledged_explains_no_replay', (await op.innerText()).includes('未重放改配'));
    check('acknowledged_snapshot_visible', (await execution.innerText()).includes(session.execution_snapshot.id));
  } else {
    check('uncertain_retains_enabled_selection', (await plugin.innerText()).includes('启用整包'));
    check('uncertain_retains_original_snapshot', (await execution.innerText()).includes(fixture.samples['direct-inherit-staged'].sessions.find(s => s.id === session.id).execution_snapshot.id));
    check('uncertain_retains_exact_candidate', (await change.innerText()).includes(session.pending_execution_change.snapshot.id));
    check('uncertain_candidate_not_effective', (await change.innerText()).includes('实际生效待核对') && (await change.innerText()).includes('启用 → 继承'));
    check('uncertain_receipt_reason_visible', (await op.innerText()).includes('hook 撤去缺少本连接的有效配置建立依据'));
    check('uncertain_blocks_input', await page.getByRole('button', { name: '发送消息' }).isDisabled());
    check('uncertain_blocks_automation_resume', await page.getByRole('button', { name: '恢复自动化', exact: true }).isDisabled());
    check('uncertain_blocks_discard', await change.getByRole('button', { name: '撤销待应用选择', exact: true }).isDisabled());
    check('uncertain_does_not_offer_reapply', await change.getByRole('button', { name: '应用已保存配置', exact: true }).count() === 0);
    await page.getByRole('button', { name: '重新核对', exact: true }).click();
    await page.evaluate(() => window.retirementEvents.listeners.change?.());
    check('reconcile_only_requests_readback', requests.filter(r => r.path === '/api/commands').every(r => r.body.kind === 'reconcile'));
  }
  await op.scrollIntoViewIfNeeded();
  await page.screenshot({ path: `.dev/w0208-hook-retirement-receipt/${mode}-1440.png`, fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: '详情', exact: true }).click();
  await op.scrollIntoViewIfNeeded();
  await page.screenshot({ path: `.dev/w0208-hook-retirement-receipt/${mode}-390.png`, fullPage: true });
  check('mobile_no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
  check('no_browser_errors', errors.length === 0);
  return { mode, checks, requests, errors, scope: 'current built UI, actual native-backed Handler exports; HTTP replay, no live listener' };
}
