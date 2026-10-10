// Production browser assets replay Application query DTOs from the Go test.
// Runtime, HTTP responses and events are controlled here; this is not native CLI evidence.
async page => {
  page = await page.context().newPage();
  const fixture = __FIXTURE__, assets = __ASSETS__, output = __OUTPUT__;
  const checks = {}, requests = [], errors = [], consoleErrors = [], unmapped = [];
  const sid = fixture.original.id, base = '/api/sessions/' + sid;
  const original = fixture.original.execution_snapshot, recovered = fixture.recovered.execution_snapshot;
  let state = 'restored', conditionMode = 'verified';
  const copy = value => JSON.parse(JSON.stringify(value));
  const check = (name, ok) => { checks[name] = Boolean(ok); if (!ok) throw Error(name); };
  const frame = () => fixture.frames[state];
  const currentID = () => page.locator('.execution-snapshot > dl dd').first().innerText();
  page.setDefaultTimeout(8000);
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()); });
  await page.addInitScript(({sid, commandID}) => {
    if (location.origin !== 'http://127.0.0.1:9473') return;
    sessionStorage.clear();
    sessionStorage.setItem('netcap.selected.v1', sid);
    Object.defineProperty(crypto, 'randomUUID', {configurable: true, value: () => commandID});
    window.EventSource = class {
      constructor() { this.listeners = {}; window.recoveryEvents = this; setTimeout(() => this.onopen?.(), 0); }
      addEventListener(name, fn) { this.listeners[name] = fn; }
      close() {}
    };
  }, {sid, commandID: fixture.command.command_id});
  await page.route('**/*', async route => {
    const request = route.request(), path = request.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    const body = request.postData() ? JSON.parse(request.postData()) : null;
    requests.push({path, body, method: request.method()});
    if (path.startsWith('/favicon.')) return route.fulfill({status: 204});
    if (path === base + '/execution/recovery-check') {
      const expected = fixture.command.execution_recovery;
      check(conditionMode + '_check_keeps_original_transaction',
        Object.keys(body).sort().join(',') === 'base_snapshot_id,operation_id,pending_change_id' &&
        body.base_snapshot_id === expected.base_snapshot_id && body.pending_change_id === expected.pending_change_id && body.operation_id === expected.operation_id);
      const response = copy(fixture.conditions);
      if (conditionMode === 'missing_echo') delete response.native.installed_recovery;
      if (conditionMode === 'wrong_version') response.native.installed_recovery.startup_source_version = '0'.repeat(64);
      if (conditionMode === 'missing_summary') delete response.installed_settings;
      if (conditionMode === 'blocked') { response.native.checks[0].status = 'blocked'; response.satisfied = false; }
      return route.fulfill({json: response});
    }
    if (path === '/api/commands') {
      const expected = fixture.command;
      check('explicit_recovery_command_keeps_original_transaction_and_reviewed_version',
        body.kind === expected.kind && body.session_id === expected.session_id && body.command_id === expected.command_id &&
        Object.keys(body.execution_recovery).sort().join(',') === Object.keys(expected.execution_recovery).sort().join(',') &&
        Object.keys(expected.execution_recovery).every(key => body.execution_recovery[key] === expected.execution_recovery[key]));
      state = 'recovering';
      return route.fulfill({status: 202, json: fixture.receipt});
    }
    if (frame()[path]) return route.fulfill({json: frame()[path]});
    if (assets[path]) return route.fulfill({path: assets[path], contentType: path.endsWith('.js') ? 'application/javascript' : path.endsWith('.css') ? 'text/css' : 'text/html'});
    unmapped.push(path);
    return route.fulfill({status: 404, body: 'Unmapped installed recovery acceptance request'});
  });
  const details = async () => {
    const root = page.locator('.execution-snapshot');
    await root.waitFor({state: 'attached'});
    if (!await root.isVisible()) await page.getByRole('button', {name: '详情', exact: true}).click();
    await root.waitFor();
    if (await root.getAttribute('open') === null) await root.locator(':scope > summary').click();
  };
  const focusPanel = async locator => {
    await locator.evaluate(element => {
      for (let parent = element.parentElement; parent; parent = parent.parentElement) {
        if (parent.scrollHeight > parent.clientHeight && /auto|scroll/.test(getComputedStyle(parent).overflowY)) {
          parent.scrollTop += element.getBoundingClientRect().top - parent.getBoundingClientRect().top - 24;
        }
      }
      window.scrollTo({top: Math.max(0, window.scrollY + element.getBoundingClientRect().top - 24)});
    });
  };
  const screenshot = async (label, locator) => { await focusPanel(locator); await page.screenshot({path: output + '/' + label + '.png'}); };
  const noOverflow = async label => check(label + '_no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
  const refresh = async () => { await page.evaluate(() => window.recoveryEvents.listeners.change()); };
  const commands = () => requests.filter(request => request.path === '/api/commands');
  try {
    check('fixture_passed_and_reviewed_source_matches_command', fixture.passed && fixture.conditions.installed_settings.source_version === fixture.command.execution_recovery.installed_source_version);
    await page.setViewportSize({width: 1440, height: 900});
    await page.goto('http://127.0.0.1:9473/');
    await details();
    check('restored_files_keep_original_snapshot_in_view', await currentID() === original.id);
    const inspection = page.getByRole('region', {name: '恢复前只读核对', exact: true});
    const inspect = inspection.getByRole('button', {name: '核对恢复条件', exact: true});
    const recover = inspection.getByRole('button', {name: '按原配置恢复查询', exact: true});
    for (const mode of ['missing_echo', 'wrong_version', 'missing_summary']) {
      conditionMode = mode;
      await inspect.click();
      await inspection.getByRole('alert').waitFor();
      check(mode + '_rejected_without_recovery_action', await recover.count() === 0 && (await inspection.innerText()).includes('恢复文件身份的版本依据不完整'));
    }
    await screenshot('rejected-1440', inspection);
    conditionMode = 'blocked';
    await inspect.click();
    await inspection.getByText('还有条件未满足，保持待核对。', {exact: true}).waitFor();
    check('blocked_conditions_cannot_recover', await recover.count() === 0);
    conditionMode = 'verified';
    await inspect.focus();
    await inspect.press('Enter');
    const version = inspection.getByRole('region', {name: '恢复文件身份版本', exact: true});
    await version.waitFor();
    check('review_explains_new_revision_and_keeps_old_history', (await version.innerText()).includes('恢复将创建配置版本 ' + recovered.revision) && (await version.innerText()).includes('旧快照进入历史，失败尝试继续保留'));
    for (const [index, path] of fixture.conditions.installed_settings.restored_files.entries()) check('review_displays_restored_file_' + index, (await version.innerText()).includes(path));
    check('review_displays_source_version', (await version.innerText()).includes(fixture.command.execution_recovery.installed_source_version));
    check('read_only_checks_never_dispatch_commands_or_change_snapshot', commands().length === 0 && await currentID() === original.id);
    check('reviewed_recovery_action_enabled', await recover.isEnabled());
    await noOverflow('review_1440');
    await screenshot('review-1440', inspection);
    await page.setViewportSize({width: 390, height: 844});
    await details();
    await noOverflow('review_390');
    await screenshot('review-390', version);
    check('mobile_recovery_action_is_reachable', await recover.isVisible() && await recover.isEnabled());
    await page.setViewportSize({width: 1440, height: 900});
    await recover.focus();
    await recover.press('Enter');
    await refresh();
    const pending = page.locator('.execution-change');
    await pending.getByText('恢复版本 ' + recovered.revision + ' 尚待确认；当前快照和失败候选继续保留。', {exact: false}).waitFor();
    check('unknown_receipt_keeps_original_snapshot', await currentID() === original.id);
    check('unknown_receipt_displays_separate_recovery_snapshot', (await pending.innerText()).includes('恢复快照 ' + recovered.id));
    check('unknown_receipt_keeps_failed_candidate', (await pending.innerText()).includes('候选快照 ' + fixture.recovered.execution_change_attempts[0].change.snapshot.id));
    check('unknown_receipt_does_not_offer_replay', await page.getByRole('button', {name: '按原配置恢复查询', exact: true}).count() === 0);
    await screenshot('uncertain-1440', pending);
    await page.reload();
    await details();
    await pending.getByText('恢复快照 ' + recovered.id, {exact: true}).waitFor();
    check('reload_keeps_pending_recovery_without_dispatch', commands().length === 1 && await currentID() === original.id);
    state = 'recovered';
    await refresh();
    await page.locator('.execution-snapshot > dl').getByText(recovered.id, {exact: true}).waitFor();
    await details();
    const binding = page.locator('.execution-snapshot > details.snapshot-asset').filter({has: page.getByText('本执行版本的插件设置文件', {exact: true})});
    await page.getByText('本执行版本的插件设置文件', {exact: true}).click();
    check('recovered_view_explains_new_identity_and_original_snapshot', (await binding.innerText()).includes('原快照 ' + original.id) && (await binding.innerText()).includes('重新记录已还原本地文件的身份'));
    check('recovered_view_uses_reviewed_source_version', (await binding.innerText()).includes(fixture.command.execution_recovery.installed_source_version));
    await noOverflow('recovered_1440');
    await screenshot('recovered-1440', binding);
    const history = page.getByRole('region', {name: '历史执行快照', exact: true});
    await history.getByText('历史版本 ' + original.revision, {exact: true}).click();
    check('original_snapshot_remains_in_history', await history.getByText(original.id, {exact: true}).isVisible());
    const attempts = page.getByRole('region', {name: '未生效的配置尝试', exact: true});
    await attempts.locator('summary').first().click();
    check('failed_candidate_and_recovery_operation_remain_visible', (await attempts.innerText()).includes(fixture.recovered.execution_change_attempts[0].change.snapshot.id) && (await attempts.innerText()).includes(fixture.receipt.operation_id));
    await screenshot('history-1440', attempts);
    await page.setViewportSize({width: 390, height: 844});
    await details();
    await noOverflow('recovered_390');
    await screenshot('recovered-390', binding);
    check('only_one_explicit_recovery_command', commands().length === 1);
    check('no_browser_exceptions', errors.length === 0);
    check('no_browser_console_errors', consoleErrors.length === 0);
    check('all_requested_assets_and_dtos_mapped', unmapped.length === 0);
    await page.close();
    return {passed: true, transport: fixture.transport, viewports: [[1440, 900], [390, 844]], checks, requests, errors, consoleErrors, unmapped};
  } catch (error) {
    await page.screenshot({path: output + '/failure.png'});
    return {passed: false, error: String(error.stack ?? error), transport: fixture.transport, checks, requests, errors, consoleErrors, unmapped, url: page.url()};
  }
}
