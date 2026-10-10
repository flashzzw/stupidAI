// Render the real Windows/Handler frames; command replies are recorded native
// receipts. This checks presentation and request identity, not another run.
async page => {
  const fixtures = __FIXTURES__, assets = __ASSETS__, output = __OUTPUT__;
  const checks = {}, commands = [], errors = [], unmapped = [], pages = [];
  const check = (name, value) => { checks[name] = !!value; if (!value) throw Error(name); };
  async function open(name, stage) {
    const fixture = fixtures[name], sid = fixture.sessions.A, path = '/api/sessions/' + sid;
    let frame = fixture.frames[stage];
    const tab = await page.context().newPage(); pages.push(tab); tab.setDefaultTimeout(8000);
    tab.on('pageerror', e => errors.push(e.message));
    await tab.addInitScript(id => {
      if (location.origin !== 'http://127.0.0.1:9469') return;
      sessionStorage.clear(); sessionStorage.setItem('netcap.selected.v1', id);
      window.EventSource = class {
        constructor() { this.listeners = {}; window.__hostChanges = () => this.listeners.change?.(); setTimeout(() => this.onopen?.(), 0); }
        addEventListener(name, listener) { this.listeners[name] = listener; }
        close() { this.listeners = {}; }
      };
    }, sid);
    await tab.route('**/*', async route => {
      const request = route.request(), key = request.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
      if (key === '/api/commands') {
        const body = request.postDataJSON(); commands.push(body);
        const saved = fixture.http.find(row => row.path === key && row.request.kind === body.kind && row.request.option_id === body.option_id);
        if (!saved) { unmapped.push(key + ':' + body.kind); return route.fulfill({status: 409, json: {error: 'Unmapped captured command'}}); }
        frame = fixture.frames.running;
        return route.fulfill({status: saved.status, json: saved.response});
      }
      if (frame[key]) return route.fulfill({json: frame[key]});
      if (assets[key]) return route.fulfill({path: assets[key], contentType: key.endsWith('.js') ? 'application/javascript' : key.endsWith('.css') ? 'text/css' : key.endsWith('.svg') ? 'image/svg+xml' : 'text/html'});
      if (key.startsWith('/favicon.')) return route.fulfill({status: 204});
      unmapped.push(key); return route.fulfill({status: 404, body: 'Unmapped captured frame'});
    });
    await tab.setViewportSize({width: 1440, height: 1000});
    await tab.goto('http://127.0.0.1:9469/');
    await tab.locator('.execution-snapshot > summary').waitFor();
    return {tab, fixture, sid, session: frame[path].session, pending: tab.locator('.pending-panel'), inspector: tab.locator('.inspector')};
  }
  async function capture(view, name) {
    await view.tab.screenshot({path: output + '/' + name + '.png'});
    check(name + '_fits_viewport', await view.tab.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
  }
  try {
    const restoring = await open('pending-go-crash', 'pending-restoring');
    check('unverified_approval_is_visible', (await restoring.pending.innerText()).includes('尚未确认有效'));
    check('unverified_approval_cannot_be_answered', await restoring.pending.getByRole('button', {name: '允许本次', exact: true}).isDisabled() && await restoring.pending.getByRole('button', {name: '拒绝本次', exact: true}).isDisabled());
    check('restoring_keeps_manual_control', await restoring.inspector.getByText('人工接管', {exact: true}).isVisible());
    await capture(restoring, 'restoring-1440');

    const recovered = await open('pending-go-crash', 'pending-after-go-restart');
    const original = recovered.fixture.samples.original_pending;
    check('recovered_original_approval_can_be_answered', await recovered.pending.getByRole('button', {name: '允许本次', exact: true}).isEnabled() && await recovered.pending.getByRole('button', {name: '拒绝本次', exact: true}).isEnabled());
    check('original_native_id_is_visible', (await recovered.inspector.innerText()).includes(recovered.session.scope.native_id));
    check('recovered_query_keeps_manual_control', await recovered.inspector.getByText('人工接管', {exact: true}).isVisible());
    await capture(recovered, 'pending-recovered-1440');
    await recovered.tab.setViewportSize({width: 390, height: 844});
    await capture(recovered, 'pending-recovered-390');
    await recovered.tab.getByRole('button', {name: '详情', exact: true}).click();
    check('mobile_shows_original_native_id', (await recovered.inspector.innerText()).includes(recovered.session.scope.native_id));
    await capture(recovered, 'pending-details-390');
    await recovered.tab.getByRole('button', {name: '关闭', exact: true}).click();
    await Promise.all([recovered.tab.waitForResponse(r => r.url().endsWith('/api/commands')),
      recovered.pending.getByRole('button', {name: '允许本次', exact: true}).click()]);
    // A real command produces an SSE change notification. Replay that signal
    // so the UI reads the captured updated Handler frame on the same page.
    await recovered.tab.evaluate(() => window.__hostChanges());
    await recovered.tab.waitForFunction(() => document.querySelector('.pending-panel')?.textContent.includes('回复已发送'));
    check('one_reply_targets_exact_original_approval', commands.length === 1 && commands[0].kind === 'respond' && commands[0].session_id === recovered.sid && commands[0].pending_id === original.id && commands[0].option_id === 'allow');
    check('submitted_reply_cannot_be_sent_again', await recovered.pending.getByRole('button', {name: '允许本次', exact: true}).isDisabled());

    for (const name of ['host-disconnect', 'go-crash', 'pending-go-crash']) {
      const running = await open(name, name === 'go-crash' ? 'after-go-restart' : 'manual');
      const summaries = await running.tab.locator('.operation > summary').allTextContents();
      check(name + '_original_send_and_reply_are_running', summaries.includes('send · 执行中') && summaries.includes('respond · 执行中') && !summaries.some(text => text.includes('待核对')));
      check(name + '_submitted_request_explains_waiting', (await running.pending.innerText()).includes('回复已发送，等待原生确认。请勿重复回答。'));
      check(name + '_submitted_request_buttons_disabled', await running.pending.getByRole('button', {name: '允许本次', exact: true}).isDisabled() && await running.pending.getByRole('button', {name: '拒绝本次', exact: true}).isDisabled());
      const operation = running.tab.locator('.operation').filter({has: running.tab.locator('code', {hasText: running.fixture.samples.original_pending.native_turn})});
      await operation.locator(':scope > summary').click();
      check(name + '_original_operation_id_visible', (await operation.innerText()).includes(running.fixture.samples.original_pending.native_turn));
      await capture(running, name + '-running-1440');
      const finished = await open(name, 'finished');
      const completed = await finished.tab.locator('.operation > summary').allTextContents();
      check(name + '_original_operations_completed', completed.includes('send · 已完成') && completed.includes('respond · 已完成'));
      check(name + '_completion_keeps_manual_control', await finished.inspector.getByText('人工接管', {exact: true}).isVisible() && await finished.inspector.getByRole('button', {name: '恢复自动化', exact: true}).isVisible());
      check(name + '_completed_request_is_not_actionable', await finished.pending.count() === 0);
      await capture(finished, name + '-finished-1440');
    }
    const offline = await open('pending-go-crash', 'pending-after-go-restart');
    await offline.tab.evaluate(() => window.dispatchEvent(new Event('offline')));
    await offline.tab.waitForFunction(() => [...document.querySelectorAll('.pending-panel button')].every(button => button.disabled));
    check('new_disconnect_disables_original_approval', await offline.pending.getByRole('button', {name: '允许本次', exact: true}).isDisabled());
    check('only_explicit_reply_was_sent', commands.length === 1);
    check('no_browser_errors', errors.length === 0); check('no_unmapped_requests', unmapped.length === 0);
    return {checks, commands, errors, unmapped, passed: Object.values(checks).every(Boolean)};
  } catch (error) {
    const last = pages.at(-1); if (last) await last.screenshot({path: output + '/failure.png'});
    return {checks, commands, errors, unmapped, passed: false, failure: String(error), last_page: last ? await last.locator('body').innerText() : ''};
  } finally { for (const tab of pages) await tab.close(); }
}
