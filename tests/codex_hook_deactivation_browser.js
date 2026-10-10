// Production snapshots and Handler exports from managed-hook deactivation.
// The Go fixture injects a stricter requirements response; HTTP timing is replayed.
async page => {
  await page.unrouteAll({ behavior: 'ignoreErrors' });
  const fixture = __FIXTURE__, assets = __ASSETS__, checks = {}, requests = [], errors = [];
  let phase = 'restricted', mode = 'disabled', label = 'disabled', activeId = fixture.sessions.original;
  page.__hookDeactivation = { checks, requests, errors };
  const check = (name, ok) => { checks[name] = !!ok; if (!ok) throw new Error(name); };
  const snapshot = () => fixture.samples[phase];
  page.on('pageerror', e => errors.push(e.message));
  await page.addInitScript(id => {
    sessionStorage.clear(); sessionStorage.setItem('netcap.selected.v1', id);
    window.EventSource = class {
      constructor() { this.listeners = {}; window.hookRemovalEvents = this; setTimeout(() => this.onopen?.(), 0); }
      addEventListener(name, fn) { this.listeners[name] = fn; }
      close() {}
    };
  }, fixture.sessions.original);
  await page.route('**/*', async route => {
    const r = route.request(), path = r.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0], body = r.postData() ? JSON.parse(r.postData()) : null;
    requests.push({ path, method: r.method(), body });
    if (path === '/api/workspace') return route.fulfill({ json: snapshot() });
    if (path === '/api/capabilities') return route.fulfill({ json: fixture.responses[path] });
    if (path.endsWith('/execution/preview')) return route.fulfill({ json: fixture.samples[label + '-preview'] });
    if (path === '/api/commands') {
      const next = { stage_execution: 'staged', prepare_execution: 'prepared', apply_execution: 'applied' }[body.kind];
      if (!next) throw new Error('unexpected mutation ' + body.kind);
      phase = label + '-' + next;
      return route.fulfill({ json: { session_id: activeId, status: body.kind === 'stage_execution' ? 'pending' : 'accepted' } });
    }
    if (path.startsWith('/api/sessions/')) {
      const s = snapshot(), id = path.slice('/api/sessions/'.length), session = s.sessions.find(row => row.id === id);
      if (session) return route.fulfill({ json: { sequence: s.sequence, session, operations: s.operations.filter(op => op.session_id === id), events: [], total_messages: session.view.messages?.length ?? 0 } });
    }
    if (assets[path]) return route.fulfill({ path: assets[path], contentType: path.endsWith('.js') ? 'application/javascript' : path.endsWith('.css') ? 'text/css' : path.endsWith('.svg') ? 'image/svg+xml' : 'text/html' });
    return route.fulfill({ status: 404, json: { error: 'unmapped fixture route' } });
  });
  await page.setViewportSize({ width: 1440, height: 1100 });
  await page.goto('http://127.0.0.1:9469/');
  const execution = page.locator('.execution-snapshot');
  await execution.locator(':scope > summary').click();
  const plugin = page.getByRole('region', { name: '本次 CLI 插件选择' }), skill = page.getByRole('region', { name: '本次原生 Skill 选择' }), form = page.locator('.execution-change');
  check('restriction_reason_visible', (await plugin.innerText()).includes('仅允许受管 hook'));
  check('restriction_blocks_input', await page.getByRole('button', { name: '发送消息' }).isDisabled());
  for (const [index, step] of [['disabled', 'disabled', fixture.sessions.original], ['inherit', 'inherit', fixture.sessions.original], ['inherit', 'direct-inherit', fixture.sessions.direct]].entries()) {
    [mode, label, activeId] = step;
    if (label === 'direct-inherit') {
      await page.locator('.session-row').filter({ hasText: 'Direct retire hook' }).click();
      await page.getByRole('heading', { name: 'Direct retire hook', exact: true }).waitFor();
      if (await execution.getAttribute('open') === null) await execution.locator(':scope > summary').click();
      check('direct_retire_has_no_other_skill_selection', await skill.count() === 0);
    }
    const before = snapshot().sessions.find(s => s.id === activeId), wasEnabled = before.scope.plugin_selections.find(p => p.asset_id === 'hooks').mode === 'enabled';
    await form.locator('[data-plugin-mode="hooks"]').selectOption(mode);
    await form.getByRole('button', { name: '预览改配差异', exact: true }).click();
    await form.getByText(/^候选版本 \d/).waitFor();
    check(label + '_explicit_diff_visible', (await form.innerText()).includes(`${wasEnabled ? '启用' : '禁用'} → ${mode === 'disabled' ? '禁用' : '继承'}`));
    const preview = requests.filter(r => r.path.endsWith('/execution/preview')).at(-1).body;
    check(label + '_preview_keeps_other_selection', (before.scope.skill_selections?.length ? preview.selections.some(s => s.asset_id === 'retained' && s.mode === 'enabled') : !preview.selections.some(s => s.asset_id === 'retained')) && preview.selections.some(s => s.asset_id === 'hooks' && s.mode === mode));
    await form.getByRole('button', { name: '保存待应用选择', exact: true }).click();
    await page.evaluate(() => window.hookRemovalEvents.listeners.change?.());
    await form.getByText(/尚未生效/).waitFor();
    check(label + '_saved_choice_not_effective', (await plugin.innerText()).includes(wasEnabled ? '启用整包' : '禁用整包'));
    const prepare = form.getByRole('button', { name: '准备应用（保持暂停）', exact: true });
    check(label + '_preparation_available', await prepare.isEnabled());
    if (wasEnabled) check(label + '_unknown_preparation_is_conditional', (await form.innerText()).includes('原配置、历史或输入归属无法确认时仍不会应用'));
    await form.scrollIntoViewIfNeeded();
    if (mode === 'disabled') await page.screenshot({ path: '.dev/w0208-hook-deactivation/pending-1440.png', fullPage: true });
    await prepare.click();
    await page.evaluate(() => window.hookRemovalEvents.listeners.change?.());
    await form.getByText(/已取得空闲依据/).waitFor();
    check(label + '_prepared_keeps_current_selection', (await plugin.innerText()).includes(wasEnabled ? '启用整包' : '禁用整包'));
    check(label + '_prepare_does_not_send_apply', requests.filter(r => r.path === '/api/commands' && r.body.kind === 'apply_execution').length === index);
    const apply = form.getByRole('button', { name: '应用已保存配置', exact: true });
    check(label + '_apply_available_after_preparation', await apply.isEnabled());
    await apply.click();
    await page.evaluate(() => window.hookRemovalEvents.listeners.change?.());
    await form.getByText(/尚未生效/).waitFor({ state: 'hidden' });
    if (before.scope.skill_selections?.length) check(label + '_other_method_keeps_selection', (await skill.innerText()).includes('retained-method · 启用'));
    else check(label + '_does_not_add_method', await skill.count() === 0);
    check(label + '_same_native_id_visible', (await page.locator('.inspector').innerText()).includes(before.scope.native_id));
    if (mode === 'disabled') check('disabled_after_apply_verified', (await plugin.innerText()).includes('禁用整包') && (await plugin.innerText()).includes('固定包、整包策略与本连接 hook 配置已核对'));
    else check(label + '_removes_current_plugin_selection', await plugin.count() === 0);
  }
  check('final_remains_paused', (await page.locator('.conversation-header').innerText()).includes('自动化已暂停'));
  check('no_message_or_automation_resume_request', requests.filter(r => r.path === '/api/commands').every(r => ['stage_execution', 'prepare_execution', 'apply_execution'].includes(r.body.kind)));
  await page.locator('.session-row').filter({ hasText: 'Original local hook' }).click();
  await page.getByRole('heading', { name: 'Original local hook', exact: true }).waitFor();
  if (await execution.getAttribute('open') === null) await execution.locator(':scope > summary').click();
  await skill.scrollIntoViewIfNeeded();
  await page.screenshot({ path: '.dev/w0208-hook-deactivation/inherited-1440.png', fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: '详情', exact: true }).click();
  await skill.scrollIntoViewIfNeeded();
  await page.screenshot({ path: '.dev/w0208-hook-deactivation/inherited-390.png', fullPage: true });
  check('mobile_no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
  check('no_browser_errors', errors.length === 0);
  return { checks, requests, errors, scope: 'built UI and native/controlled-policy production snapshots; HTTP replay, no live listener or native managed-enforcement claim' };
}
