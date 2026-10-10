// Replay real native Application snapshots at the durable acknowledgement boundary.
async page => {
  const fixture = __FIXTURE__, assets = __ASSETS__, checks = {}, requests = [], errors = [];
  let state = fixture.acknowledged;
  const check = (name, value) => { checks[name] = !!value; if (!value) throw Error(name); };
  page.setDefaultTimeout(6000);
  await page.addInitScript(id => {
    sessionStorage.setItem('netcap.selected.v1', id);
    window.EventSource = class { constructor() { this.listeners = {}; window.recoveryEvents = this; setTimeout(() => this.onopen?.(), 0); } addEventListener(k, f) { this.listeners[k] = f; } close() {} };
  }, fixture.sid);
  page.on('pageerror', error => errors.push(error.message));
  await page.route('**/*', async route => {
    const request = route.request(), path = request.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    const body = request.postData() ? JSON.parse(request.postData()) : null;
    requests.push({path, method: request.method(), body});
    if (path === '/api/workspace') return route.fulfill({json: state});
    if (path === '/api/commands') {
      if (body.kind !== 'resume' || body.session_id !== fixture.sid) return route.fulfill({status: 409, json: {error: 'unexpected command'}});
      state = fixture.following;
      await route.fulfill({json: {command_id: body.command_id, session_id: fixture.sid, status: 'accepted'}});
      await page.evaluate(() => window.recoveryEvents.listeners.change?.());
      return;
    }
    if (path.startsWith('/api/sessions/')) {
      const id = path.slice('/api/sessions/'.length), session = state.sessions.find(s => s.id === id);
      if (session) return route.fulfill({json: {sequence: state.sequence, session, operations: state.operations.filter(o => o.session_id === id), events: [], total_messages: session.view.messages.length}});
    }
    if (assets[path]) return route.fulfill({path: assets[path], contentType: path.endsWith('.js') ? 'application/javascript' : path.endsWith('.css') ? 'text/css' : 'text/html'});
    return route.fulfill({status: 404, json: {error: 'unmapped replay route'}});
  });
  const openSnapshot = async () => page.locator('.execution-snapshot > summary').click();
  const banner = page.getByText('原配置恢复已确认；原线程当前未加载，能力仍待核对。请明确恢复自动化后继续。', {exact: true});
  await page.setViewportSize({width: 1440, height: 1100});
  await page.goto('http://127.0.0.1:9467/'); await openSnapshot();
  await page.getByRole('textbox', {name: '消息', exact: true}).fill('Owned UI draft; do not send');
  check('acknowledgement_not_premature_success', await banner.count() === 0);
  check('pending_recovery_not_repeated', await page.getByRole('button', {name: '按原配置恢复原线程', exact: true}).count() === 0);
  check('pending_input_disabled', await page.getByRole('button', {name: '发送消息', exact: false}).isDisabled());
  state = fixture.after; await page.reload(); await openSnapshot(); await banner.waitFor();
  await page.getByRole('textbox', {name: '消息', exact: true}).fill('Owned UI draft; do not send');
  check('historical_success_current_unloaded_explained', await banner.isVisible());
  check('no_automatic_command', requests.every(r => r.path !== '/api/commands'));
  check('original_snapshot_preserved', await page.locator('.execution-snapshot').getByText(fixture.snapshot, {exact: true}).count() === 1);
  const archive = page.getByRole('region', {name: '未生效的配置尝试'});
  await archive.locator(':scope > details > summary').click();
  check('failed_candidate_preserved', (await archive.innerText()).includes(fixture.candidate));
  check('current_plugin_unverified', (await page.getByRole('region', {name: '本次 CLI 插件选择'}).innerText()).includes('待核对'));
  check('current_skill_unverified', (await page.getByRole('region', {name: '本次原生 Skill 选择'}).innerText()).includes('待核对'));
  check('unloaded_input_disabled', await page.getByRole('button', {name: '发送消息', exact: false}).isDisabled());
  const resume = page.getByRole('button', {name: '恢复自动化', exact: true});
  check('explicit_resume_available', await resume.isEnabled());
  const op = page.locator('.operation').filter({has: page.locator('summary', {hasText: 'recover_execution'})});
  await op.locator('summary').click();
  check('durable_confirmation_visible', (await op.innerText()).includes('已保存的原配置恢复确认'));
  await banner.scrollIntoViewIfNeeded(); await page.screenshot({path: '.dev/w0208-codex-durable-recovery/unloaded-1440.png'});
  await page.setViewportSize({width: 390, height: 844}); await page.getByRole('button', {name: '详情', exact: true}).click(); await banner.scrollIntoViewIfNeeded();
  check('mobile_no_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
  await page.screenshot({path: '.dev/w0208-codex-durable-recovery/unloaded-390.png'});
  await resume.click(); await page.getByRole('button', {name: '暂停自动化', exact: true}).waitFor();
  check('one_explicit_resume', requests.filter(r => r.path === '/api/commands').length === 1 && requests.find(r => r.path === '/api/commands').body.kind === 'resume');
  check('loading_hint_removed_after_explicit_resume', await banner.count() === 0);
  check('original_snapshot_after_resume', await page.locator('.execution-snapshot').getByText(fixture.snapshot, {exact: true}).count() === 1);
  check('archive_retained_after_resume', await archive.count() === 1);
  await page.getByRole('textbox', {name: '消息', exact: true}).fill('Owned UI draft; do not send');
  check('input_available_after_native_verification', await page.getByRole('button', {name: '发送消息', exact: false}).isEnabled());
  check('draft_never_sent', requests.filter(r => r.path === '/api/commands').every(r => r.body.kind === 'resume'));
  check('no_page_errors', errors.length === 0);
  return {checks, requests, errors, scope: 'Real App snapshots replayed at durable acknowledgement, unloaded confirmation and subsequent explicit resume; no native execution from browser.'};
}
