// Render the four captured production Handler states with the built UI.
// HTTP and SSE are replayed; native input behavior is proved by the probe/audit.
async page => {
  page = await page.context().newPage();
  const fixture = __FIXTURE__, assets = __ASSETS__, output = __OUTPUT__;
  const checks = {}, requests = [], errors = [], consoleErrors = [], unmapped = [];
  const sid = fixture.session_id, detail = '/api/sessions/' + sid, observe = detail + '/native/observe', history = detail + '/native/history';
  let stage = 'two-client-idle';
  const check = (name, ok) => { checks[name] = Boolean(ok); if (!ok) throw Error(name); };
  const samples = () => requests.filter(request => request.path === observe);
  page.setDefaultTimeout(10000);
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()); });
  await page.addInitScript(sid => {
    if (location.origin !== 'http://127.0.0.1:9476') return;
    sessionStorage.clear(); sessionStorage.setItem('netcap.selected.v1', sid);
    window.EventSource = class {
      constructor() { this.listeners = {}; window.nativeInputEvents = this; setTimeout(() => this.onopen?.(), 0); }
      addEventListener(name, fn) { this.listeners[name] = fn; }
      close() {}
    };
  }, sid);
  await page.route('**/*', async route => {
    const request = route.request(), path = request.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    const body = request.postData() ? JSON.parse(request.postData()) : null;
    requests.push({path, body, method:request.method(), stage});
    if (path.startsWith('/favicon.')) return route.fulfill({status:204});
    if (path === observe) check(stage + '_samples_exact_original_binding', request.method() === 'POST' && JSON.stringify(body) === '{}');
    if (path === history) check(stage + '_history_reads_exact_original_binding', request.method() === 'POST' && JSON.stringify(body) === '{"cursor":""}');
    if (fixture.frames[stage][path]) return route.fulfill({json:fixture.frames[stage][path]});
    if (assets[path]) return route.fulfill({path:assets[path], contentType:path.endsWith('.js') ? 'application/javascript' : path.endsWith('.css') ? 'text/css' : 'text/html'});
    unmapped.push(path); return route.fulfill({status:404, body:'Unmapped native input observation request'});
  });
  try {
    check('fixture_retains_counterexample_and_incomplete_acceptance', fixture.observed_checks_passed &&
      fixture.input_exclusivity === 'violated' && fixture.goal_acceptance_met === false);
    await page.setViewportSize({width:1440, height:1000});
    await page.goto('http://127.0.0.1:9476/');
    const panel = page.getByRole('region', {name:'原执行只读观察', exact:true});
    const transcript = page.getByRole('region', {name:'原生历史只读查看', exact:true});
    await panel.waitFor();
    check('binding_is_explicitly_readonly', (await page.locator('.conversation-header').innerText()).includes('外部原生只读绑定'));
    check('observation_requires_explicit_start', samples().length === 0);
    for (const next of ['two-client-idle', 'approval-pending', 'tool-running', 'tool-finished']) {
      stage = next;
      await page.setViewportSize({width:1440, height:1000});
      await page.evaluate(() => window.nativeInputEvents.listeners.change());
      const received = page.waitForResponse(response => response.url().endsWith(observe) && response.request().method() === 'POST');
      await panel.getByRole('button', {name:'开始只读观察', exact:true}).click();
      check(stage + '_response_is_exact_captured_handler_dto', JSON.stringify(await (await received).json()) === JSON.stringify(fixture.frames[stage][observe]));
      await panel.getByText(/原生清单已采样 · 控制待核对/).waitFor();
      await transcript.getByRole('button', {name:'从头重新读取历史', exact:true}).click();
      await transcript.locator('.native-history-entry').first().waitFor();
      check(stage + '_original_history_remains_readable', (await transcript.innerText()).includes('NETCAP_NATIVE_INPUT=seed_a'));
      const roster = panel.getByRole('region', {name:'Claude 原生清单声明', exact:true});
      const sample = fixture.frames[stage][observe], declared = sample.view.agent_roster;
      await roster.getByText(fixture.native_id, {exact:true}).waitFor();
      const field = label => roster.locator('dt').filter({hasText:label}).locator('xpath=following-sibling::dd[1]').innerText();
      check(stage + '_full_id_and_short_selector_distinct', await field('完整会话 ID') === fixture.native_id && await field('短作业 ID') === declared.job_id);
      check(stage + '_roster_is_displayed_as_a_declaration', await field('清单中的 PID') === String(declared.pid) &&
        await field('任务状态 state') === declared.state && await field('进程状态 status') === declared.status &&
        await field('等待原因 waitingFor') === (declared.waiting_for || '未返回'));
      check(stage + '_incomplete_execution_and_draft_limit_are_visible', (await panel.innerText()).includes('本次执行范围查询不完整') &&
        (await panel.innerText()).includes('不能排除尚未收到的审批或终端草稿'));
      check(stage + '_unknown_session_does_not_gain_controls', (await page.locator('.conversation-header').innerText()).includes('待核对') &&
        await page.locator('#message').count() === 0 && await page.getByRole('button', {name:/^(发送|恢复自动化|转人工|停止当前轮次|允许|拒绝)$/}).count() === 0);
      check(stage + '_desktop_has_no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      await roster.scrollIntoViewIfNeeded();
      await page.screenshot({path:output + '/' + stage + '-1440.png'});
      await page.setViewportSize({width:390, height:844});
      check(stage + '_narrow_view_has_no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      check(stage + '_narrow_title_and_details_stay_in_view', await page.locator('.conversation-header').evaluate(element =>
        [...element.querySelectorAll('h1,button')].every(child => child.getBoundingClientRect().right <= innerWidth)));
      await roster.scrollIntoViewIfNeeded();
      await page.screenshot({path:output + '/' + stage + '-390.png'});
      await panel.getByRole('button', {name:'停止观察', exact:true}).click();
      await panel.getByText(/采样已停止或不可用 · 以下为上次记录/).waitFor();
      check(stage + '_stopped_sample_is_marked_stale', await panel.getByRole('button', {name:'开始只读观察', exact:true}).isEnabled());
    }
    const count = samples().length;
    await page.waitForTimeout(3200);
    check('stopping_observation_cancels_automatic_sampling', samples().length === count);
    check('no_execution_command_was_sent', !requests.some(request => request.path === '/api/commands'));
    check('no_native_attach_or_resume_was_requested', !requests.some(request => /native\/(attach|resume)/.test(request.path)));
    check('no_browser_exceptions', errors.length === 0);
    check('no_console_errors', consoleErrors.length === 0);
    check('all_assets_and_actual_handler_dtos_mapped', unmapped.length === 0);
    await page.close();
    return {passed:true, checks, requests, errors, consoleErrors, unmapped,
      scope:'built UI and four actual Handler DTOs; replayed HTTP/SSE; no native control or additional model request'};
  } catch (error) {
    await page.screenshot({path:output + '/failure.png'});
    await page.close();
    return {passed:false, error:String(error.stack ?? error), checks, requests, errors, consoleErrors, unmapped};
  }
}
