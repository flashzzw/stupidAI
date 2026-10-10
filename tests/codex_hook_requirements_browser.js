// Native no-requirements control and stricter policy-response boundary samples.
// Responses were exported by production Application/Handler; HTTP is replayed.
async page => {
  await page.unrouteAll({ behavior: 'ignoreErrors' });
  const fixture = __FIXTURE__, assets = __ASSETS__, checks = {}, requests = [], errors = [];
  let phase = 'before';
  const check = (name, ok) => { checks[name] = !!ok; if (!ok) throw new Error(name); };
  page.__managedHooks = { checks, requests, errors };
  page.on('pageerror', e => errors.push(e.message));
  const snapshot = () => phase === 'before' ? fixture.samples['before-restriction'] : fixture.responses['/api/workspace'];
  await page.addInitScript(id => {
    sessionStorage.clear(); sessionStorage.setItem('netcap.selected.v1', id);
    window.EventSource = class {
      constructor() { this.listeners = {}; window.managedHookEvents = this; setTimeout(() => this.onopen?.(), 0); }
      addEventListener(name, fn) { this.listeners[name] = fn; }
      close() {}
    };
  }, fixture.sessions.original);
  await page.route('**/*', async route => {
    const request = route.request(), path = request.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    requests.push({ path, method: request.method() });
    if (path === '/api/workspace') return route.fulfill({ json: snapshot() });
    if (path.startsWith('/api/sessions/') && phase === 'before') {
      const s = snapshot(), id = path.slice('/api/sessions/'.length), session = s.sessions.find(row => row.id === id);
      if (session) return route.fulfill({ json: { sequence: s.sequence, session, operations: s.operations.filter(op => op.session_id === id), events: [], total_messages: session.view.messages?.length ?? 0 } });
    }
    if (fixture.responses[path]) return route.fulfill({ json: fixture.responses[path] });
    if (assets[path]) return route.fulfill({ path: assets[path], contentType: path.endsWith('.js') ? 'application/javascript' : path.endsWith('.css') ? 'text/css' : 'text/html' });
    return route.fulfill({ status: 404, json: { error: 'unmapped fixture route' } });
  });
  await page.setViewportSize({ width: 1440, height: 1100 });
  await page.goto('http://127.0.0.1:9468/');
  const openExecution = async () => {
    const details = page.locator('.execution-snapshot');
    if (await details.getAttribute('open') === null) await details.locator(':scope > summary').click();
  };
  await openExecution();
  const plugin = page.getByRole('region', { name: '本次 CLI 插件选择' });
  check('native_control_is_verified', (await plugin.innerText()).includes('固定包、整包策略与本连接 hook 配置已核对'));
  check('native_control_has_explicit_trust', (await plugin.innerText()).includes('本版本 hook：已明确信任'));
  phase = 'restricted';
  await page.evaluate(() => window.managedHookEvents.listeners.change?.());
  await plugin.getByText(/仅允许受管 hook/).waitFor();
  check('policy_change_is_specific', (await plugin.innerText()).includes('信任审核不能覆盖'));
  check('policy_change_withdraws_verified_label', (await plugin.innerText()).includes('原生包与组件待核对') && !(await plugin.innerText()).includes('固定包、整包策略与本连接 hook 配置已核对'));
  check('saved_trust_not_silently_rewritten', (await plugin.innerText()).includes('本版本 hook：已明确信任'));
  check('original_input_blocked', await page.getByRole('button', { name: '发送消息' }).isDisabled());
  const initial = fixture.samples['before-restriction'].sessions.find(s => s.id === fixture.sessions.original);
  check('original_snapshot_and_id_preserved', (await page.locator('.inspector').innerText()).includes(initial.execution_snapshot.id) && (await page.locator('.inspector').innerText()).includes(initial.scope.native_id));
  await plugin.scrollIntoViewIfNeeded();
  await page.screenshot({ path: '.dev/w0208-managed-hooks/policy-changed-1440.png', fullPage: true });
  await page.locator('.session-row').filter({ hasText: 'Managed policy rejected' }).click();
  await page.getByRole('heading', { name: 'Managed policy rejected', exact: true }).waitFor();
  const op = page.locator('.operation').filter({ hasText: '确认未发送' });
  await op.locator('summary').click();
  check('denial_is_not_sent_not_unknown', (await op.innerText()).includes('确认未发送'));
  check('denial_reason_visible', (await op.innerText()).includes('仅允许受管 hook') && (await op.innerText()).includes('信任审核不能覆盖'));
  check('denied_has_no_native_id', (await page.locator('.inspector').innerText()).includes('尚未取得'));
  check('denied_input_disabled', await page.getByRole('button', { name: '发送消息' }).isDisabled());
  await op.scrollIntoViewIfNeeded();
  await page.screenshot({ path: '.dev/w0208-managed-hooks/rejected-1440.png', fullPage: true });
  await page.locator('.session-row').filter({ hasText: 'Disabled local hook' }).click();
  await page.getByRole('heading', { name: 'Disabled local hook', exact: true }).waitFor();
  await openExecution();
  check('disabled_package_remains_supported', (await plugin.innerText()).includes('禁用整包') && (await plugin.innerText()).includes('固定包、整包策略与本连接 hook 配置已核对'));
  check('disabled_does_not_claim_execution', (await plugin.innerText()).includes('实际执行结果另行记录'));
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: '详情', exact: true }).click();
  await plugin.scrollIntoViewIfNeeded();
  await page.screenshot({ path: '.dev/w0208-managed-hooks/disabled-390.png', fullPage: true });
  check('mobile_no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
  check('viewing_policy_never_mutates_configuration', requests.every(r => r.method === 'GET'));
  check('no_browser_errors', errors.length === 0);
  return { checks, requests, errors, scope: 'built UI and production Handler exports; managed-only response injected in Go test, HTTP replay; no native managed-enforcement claim' };
}
