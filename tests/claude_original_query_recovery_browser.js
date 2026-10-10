// Built production UI with actual Application/SQLite query DTOs.
// Browser HTTP/event responses and Runtime are controlled; no native CLI claim.
async page => {
  page = await page.context().newPage();
  const fixture = __FIXTURE__, assets = __ASSETS__, output = __OUTPUT__;
  const checks = {}, requests = [], errors = [], consoleErrors = [], unmapped = [];
  const sid = fixture.original.id, base = '/api/sessions/' + sid;
  let state = 'failed', conditionMode = 'verified';
  const copy = value => JSON.parse(JSON.stringify(value));
  const check = (name, ok) => { checks[name] = Boolean(ok); if (!ok) throw Error(name); };
  const frame = () => fixture.frames[state];
  const currentID = () => page.locator('.execution-snapshot > dl dd').first().innerText();
  page.setDefaultTimeout(8000);
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()); });
  await page.addInitScript(({sid, commandID}) => {
    if (location.origin !== 'http://127.0.0.1:9475') return;
    sessionStorage.clear(); sessionStorage.setItem('netcap.selected.v1', sid);
    Object.defineProperty(crypto, 'randomUUID', {configurable:true, value:() => commandID});
    window.EventSource = class {
      constructor() { this.listeners = {}; window.originalRecoveryEvents = this; setTimeout(() => this.onopen?.(), 0); }
      addEventListener(name, fn) { this.listeners[name] = fn; }
      close() {}
    };
  }, {sid, commandID:fixture.command.command_id});
  await page.route('**/*', async route => {
    const request = route.request(), path = request.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    const body = request.postData() ? JSON.parse(request.postData()) : null;
    requests.push({path, body, method:request.method()});
    if (path.startsWith('/favicon.')) return route.fulfill({status:204});
    if (path === base + '/execution/recovery-check') {
      const expected = fixture.command.execution_recovery;
      check(conditionMode + '_read_keeps_exact_original_references', Object.keys(body).sort().join(',') === 'base_snapshot_id,operation_id,pending_change_id' &&
        ['base_snapshot_id','operation_id','pending_change_id'].every(key => body[key] === expected[key]));
      const response = copy(fixture.conditions);
      if (conditionMode === 'missing_echo') delete response.native.original_query_retry;
      if (conditionMode === 'missing_mode') delete response.original_query_retry;
      if (conditionMode === 'unknown_version') response.native.original_query_retry.version = 2;
      if (conditionMode === 'wrong_failure') response.native.original_query_retry.failure.operation = 'different';
      if (conditionMode === 'wrong_stage') response.native.original_query_retry.failure.stage = 'open_candidate';
      if (conditionMode === 'wrong_time') response.native.original_query_retry.failure.at = '2020-01-01T00:00:00Z';
      if (conditionMode === 'unconfirmed_exit') response.native.original_query_retry.failure.cleanup = 'unconfirmed';
      if (conditionMode === 'blocked') { response.native.checks[0].status = 'blocked'; response.satisfied = false; }
      return route.fulfill({json:response});
    }
    if (path === '/api/commands') {
      const expected = fixture.command;
      check('explicit_command_keeps_exact_references_and_original_query_mode', body.kind === 'recover_execution' && body.session_id === expected.session_id && body.command_id === expected.command_id &&
        Object.keys(body.execution_recovery).sort().join(',') === Object.keys(expected.execution_recovery).sort().join(',') &&
        Object.keys(expected.execution_recovery).every(key => body.execution_recovery[key] === expected.execution_recovery[key]) && body.execution_recovery.original_query_retry === true);
      state = 'recovering';
      return route.fulfill({status:202, json:fixture.receipt});
    }
    if (frame()[path]) return route.fulfill({json:frame()[path]});
    if (assets[path]) return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    unmapped.push(path); return route.fulfill({status:404,body:'Unmapped original recovery acceptance request'});
  });
  const details = async () => {
    const root = page.locator('.execution-snapshot');
    await root.waitFor({state:'attached'});
    if (!await root.isVisible()) await page.getByRole('button', {name:'详情',exact:true}).click();
    await root.waitFor();
    if (await root.getAttribute('open') === null) await root.locator(':scope > summary').click();
  };
  const screenshot = async (label, locator) => {
    await locator.evaluate(element => {
      for (let parent = element.parentElement; parent; parent = parent.parentElement) {
        if (parent.scrollHeight > parent.clientHeight && /auto|scroll/.test(getComputedStyle(parent).overflowY)) parent.scrollTop += element.getBoundingClientRect().top - parent.getBoundingClientRect().top - 24;
      }
      window.scrollTo({top:Math.max(0,window.scrollY + element.getBoundingClientRect().top - 24)});
    });
    await page.screenshot({path:output + '/' + label + '.png'});
  };
  const noOverflow = async label => check(label + '_no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
  const refresh = async () => page.evaluate(() => window.originalRecoveryEvents.listeners.change());
  const commands = () => requests.filter(request => request.path === '/api/commands');
  try {
    check('application_fixture_passed_with_explicit_recovery_mode', fixture.passed && fixture.conditions.original_query_retry && fixture.command.execution_recovery.original_query_retry === true);
    await page.setViewportSize({width:1440,height:900});
    await page.goto('http://127.0.0.1:9475/');
    await details();
    check('failure_keeps_original_snapshot', await currentID() === fixture.original.execution_snapshot.id);
    const inspection = page.getByRole('region', {name:'恢复前只读核对',exact:true});
    const inspect = inspection.getByRole('button', {name:'核对恢复条件',exact:true});
    const recover = inspection.getByRole('button', {name:'按原配置重开并核对能力',exact:true});
    for (const mode of ['missing_echo','missing_mode','unknown_version','wrong_failure','wrong_stage','wrong_time','unconfirmed_exit']) {
      conditionMode = mode; await inspect.click();
      await inspection.getByRole('alert').waitFor();
      check(mode + '_cannot_offer_a_recovery_action', await recover.count() === 0 && await inspection.getByRole('button', {name:'按原配置恢复查询',exact:true}).count() === 0);
    }
    await screenshot('rejected-1440',inspection);
    conditionMode = 'blocked'; await inspect.click();
    await inspection.getByText('还有条件未满足，保持待核对。', {exact:true}).waitFor();
    check('blocked_conditions_keep_recovery_unavailable', await recover.count() === 0);
    conditionMode = 'verified'; await inspect.focus(); await inspect.press('Enter');
    const mode = inspection.getByRole('region', {name:'候选启动前的恢复',exact:true});
    await mode.waitFor();
    check('review_explains_candidate_not_started_and_original_id', (await mode.innerText()).includes('候选尚未启动') && (await mode.innerText()).includes('按原 ID 重开原配置并核对能力'));
    check('review_explains_missing_prior_inventory_and_separate_result', (await mode.innerText()).includes('没有可比较的失败前能力清单') && (await mode.innerText()).includes('单独记录首次成功核对的结果'));
    check('inspection_does_not_start_or_promote', commands().length === 0 && await currentID() === fixture.original.execution_snapshot.id);
    check('reviewed_original_query_action_is_enabled', await recover.isEnabled());
    check('no_unneeded_file_identity_version', await inspection.getByRole('region', {name:'恢复文件身份版本',exact:true}).count() === 0);
    await noOverflow('review_1440'); await screenshot('review-1440',inspection);
    await page.setViewportSize({width:390,height:844}); await details();
    await noOverflow('review_390'); await screenshot('review-390',mode);
    check('narrow_screen_recovery_action_is_reachable', await recover.isVisible() && await recover.isEnabled());
    await page.setViewportSize({width:1440,height:900});
    await recover.focus(); await recover.press('Enter'); await refresh();
    const pending = page.locator('.execution-change');
    await pending.getByText('按原配置恢复中 · 结果待核对，自动化仍暂停；不会重复启动。', {exact:true}).waitFor();
    check('unknown_receipt_keeps_original_snapshot', await currentID() === fixture.original.execution_snapshot.id);
    check('unknown_receipt_keeps_failed_candidate', (await pending.innerText()).includes(fixture.recovered.execution_change_attempts[0].change.snapshot.id));
    check('unknown_receipt_does_not_offer_replay', await recover.count() === 0);
    await screenshot('uncertain-1440',pending);
    await page.reload(); await details();
    await pending.getByText('按原配置恢复中 · 结果待核对，自动化仍暂停；不会重复启动。', {exact:true}).waitFor();
    check('page_reload_does_not_redispatch', commands().length === 1 && await currentID() === fixture.original.execution_snapshot.id);
    state = 'recovered'; await refresh();
    const attempts = page.getByRole('region', {name:'未生效的配置尝试',exact:true});
    await attempts.waitFor();
    await attempts.locator('summary').first().click();
    check('recovered_original_snapshot_is_unchanged', await currentID() === fixture.original.execution_snapshot.id);
    check('candidate_and_recovery_operation_remain_reviewable', (await attempts.innerText()).includes(fixture.recovered.execution_change_attempts[0].change.snapshot.id) && (await attempts.innerText()).includes(fixture.receipt.operation_id));
    check('historical_failure_still_names_original_query_start', (await attempts.innerText()).includes('重开原查询'));
    check('completed_recovery_removes_pending_change', await pending.getByText('按原配置恢复中 · 结果待核对，自动化仍暂停；不会重复启动。', {exact:true}).count() === 0 && await pending.getByText('候选快照 ' + fixture.recovered.execution_change_attempts[0].change.snapshot.id, {exact:true}).count() === 0);
    check('recovered_application_remains_paused', fixture.recovered.paused && !fixture.recovered.execution_hold);
    await noOverflow('recovered_1440'); await screenshot('recovered-1440',attempts);
    await page.setViewportSize({width:390,height:844}); await details();
    await noOverflow('recovered_390'); await screenshot('recovered-390',attempts);
    check('only_one_explicit_recovery_command', commands().length === 1);
    check('no_browser_exceptions', errors.length === 0);
    check('no_browser_console_errors', consoleErrors.length === 0);
    check('all_assets_and_application_dtos_mapped', unmapped.length === 0);
    await page.close();
    return {passed:true,transport:fixture.transport,mode:fixture.mode,checks,requests,errors,consoleErrors,unmapped};
  } catch (error) {
    await page.screenshot({path:output + '/failure.png'});
    return {passed:false,error:String(error.stack ?? error),transport:fixture.transport,mode:fixture.mode,checks,requests,errors,consoleErrors,unmapped,url:page.url()};
  }
}
