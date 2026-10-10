// Replay real native Application/Handler frames. Gap and unknown-source
// variants exercise presentation only; command replies remain captured data.
async page => {
  const fixtures = __FIXTURES__, assets = __ASSETS__, output = __OUTPUT__;
  const checks = {}, commands = [], errors = [], unmapped = [], pages = [];
  const check = (name, ok) => {checks[name] = !!ok; if (!ok) throw Error(name);};
  const clone = value => JSON.parse(JSON.stringify(value));
  async function open(name, stage = 'after-allow', label = 'A', variant = '') {
    const fixture = fixtures[name], sid = fixture.sessions[label], base = '/api/sessions/' + sid;
    const tab = await page.context().newPage(); pages.push(tab); tab.setDefaultTimeout(8000);
    let frame = clone(fixture.frames[stage]);
    const session = frame[base].session, history = session.view.configuration_notices;
    if (variant === 'unknown') {
      history.items[0].source = 'future_中文\n\u202E';
      history.items[0].path = '/owned/path\n\u202E.json';
    }
    if (variant === 'optional') {
      history.items[0].source = '__proto__'; history.items[0].path = ''; delete history.items[0].prompt_id;
    }
    if (variant === 'gap') history.recording_failed = true;
    if (variant === 'unregistered') history.registered = false;
    if (variant === 'legacy') delete session.view.configuration_notices;
    if (variant === 'truncated') {
      history.truncated = true; history.current_count = 131;
      history.items = Array.from({length: 128}, (_, n) => ({...history.items[0], id: 'display-notice-' + n}));
    }
    if (variant) {
      const listed = frame['/api/workspace'].sessions.find(s => s.id === sid);
      if (listed?.view) listed.view.configuration_notices = clone(session.view.configuration_notices ?? null);
    }
    tab.on('pageerror', error => errors.push(error.message));
    await tab.addInitScript(id => {
      if (location.origin !== 'http://127.0.0.1:9468') return;
      sessionStorage.clear(); sessionStorage.setItem('netcap.selected.v1', id);
      window.EventSource = class {constructor() {setTimeout(() => this.onopen?.(), 0);} addEventListener() {} close() {}};
    }, sid);
    await tab.route('**/*', async route => {
      const request = route.request(), path = request.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
      if (path === '/api/commands') {
        const body = request.postDataJSON(); commands.push({name, label, variant, body});
        const expected = fixture.http.find(row => row.path === path && row.request.kind === body.kind && row.request.option_id === body.option_id);
        if (!expected) {unmapped.push(name + ':' + JSON.stringify(body)); return route.fulfill({status: 409, json: {error: 'Unexpected notification command'}});}
        frame = clone(fixture.frames.finished);
        return route.fulfill({status: expected.status, json: expected.response});
      }
      if (frame[path]) return route.fulfill({json: frame[path]});
      if (assets[path]) return route.fulfill({path: assets[path], contentType: path.endsWith('.js') ? 'application/javascript' : path.endsWith('.css') ? 'text/css' : path.endsWith('.svg') ? 'image/svg+xml' : 'text/html'});
      if (path.startsWith('/favicon.')) return route.fulfill({status: 204});
      unmapped.push(name + ':' + path); return route.fulfill({status: 404, body: 'Unmapped notification fixture'});
    });
    await tab.setViewportSize({width: 1440, height: 1000}); await tab.goto('http://127.0.0.1:9468/');
    const summary = tab.locator('.execution-snapshot > summary'); await summary.waitFor();
    return {tab, sid, session, summary, notices: tab.locator('.configuration-notices'), pending: tab.locator('.pending-panel')};
  }
  async function details(view) {
    await view.summary.click(); await view.notices.locator(':scope > summary').click();
  }
  async function capture(view, name) {
    await view.notices.scrollIntoViewIfNeeded(); await view.tab.screenshot({path: output + '/' + name + '.png'});
    check(name + '_no_horizontal_overflow', await view.tab.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
  }
  async function refuse(view) {
    const original = view.session.view.pending.find(p => p.status === 'pending');
    await Promise.all([view.tab.waitForResponse(r => r.url().endsWith('/api/commands')),
      view.pending.getByRole('button', {name: '拒绝本次', exact: true}).click()]);
    check('refuse_' + view.sid + '_uses_original_request', commands.at(-1)?.body.pending_id === original.id && commands.at(-1)?.body.session_id === view.sid && commands.at(-1)?.body.option_id === 'deny');
  }
  try {
    const policy = await open('policy-notice');
    check('current_notice_count_visible_without_expanding_snapshot', (await policy.summary.innerText()).includes('当前查询收到 1 条配置通知'));
    check('notice_invalidates_old_allow_but_keeps_deny', await policy.pending.getByRole('button', {name: '允许本次', exact: true}).isDisabled() && await policy.pending.getByRole('button', {name: '拒绝本次', exact: true}).isEnabled());
    check('approval_reason_does_not_claim_changed_rules', (await policy.pending.innerText()).includes('配置变化通知') && (await policy.pending.innerText()).includes('不证明权限规则已变更'));
    await details(policy);
    const notice = fixtures['policy-notice'].observations['original-notice'];
    check('original_receipt_input_and_snapshot_visible', (await policy.notices.innerText()).includes(notice.receipt_operation) && (await policy.notices.innerText()).includes(notice.receipt_snapshot_id));
    check('receipt_position_is_not_causal_attribution', (await policy.notices.innerText()).includes('不证明该输入受到配置影响'));
    await policy.notices.locator('.snapshot-asset details > summary').click();
    check('native_prompt_and_query_remain_distinct', (await policy.notices.innerText()).includes(notice.prompt_id) && (await policy.notices.innerText()).includes(notice.query_id) && notice.prompt_id !== notice.receipt_operation);
    check('notification_limitations_visible', (await policy.notices.innerText()).includes('不证明权限规则已变化、配置已应用或已被阻止') && (await policy.notices.innerText()).includes('不读取文件正文'));
    await capture(policy, 'policy-notice-1440');
    await policy.tab.setViewportSize({width: 390, height: 844});
    await policy.tab.getByRole('button', {name: '详情', exact: true}).click();
    await capture(policy, 'policy-notice-390');
    await policy.tab.getByRole('button', {name: '关闭', exact: true}).click();
    await refuse(policy);

    for (const name of ['policy-disabled-notice', 'policy-managed-notice', 'user-notice', 'project-notice', 'local-notice']) {
      const view = await open(name); await details(view);
      check(name + '_native_registration_and_count_visible', (await view.notices.innerText()).includes('原生已确认当前查询的通知回调登记') && (await view.notices.innerText()).includes('当前查询通知 1 条'));
      check(name + '_allow_disabled_and_refusal_available', await view.pending.getByRole('button', {name: '允许本次', exact: true}).isDisabled() && await view.pending.getByRole('button', {name: '拒绝本次', exact: true}).isEnabled());
      if (name === 'user-notice' || name === 'local-notice') {await capture(view, name + '-1440'); await refuse(view);}
    }
    const sibling = await open('policy-notice', 'after-allow', 'B'); await details(sibling);
    check('idle_sibling_keeps_its_own_notice', (await sibling.notices.innerText()).includes('当前查询通知 1 条') && !(await sibling.notices.innerText()).includes(notice.receipt_operation));
    check('idle_sibling_has_no_invented_input_snapshot', (await sibling.notices.innerText()).includes('未关联通知接收时的工作台输入') && !(await sibling.notices.innerText()).includes(notice.receipt_snapshot_id));
    await sibling.notices.locator('.snapshot-asset details > summary').click();
    check('idle_sibling_missing_native_prompt_explained', (await sibling.notices.innerText()).includes('本通知没有原生提示 ID'));
    const empty = await open('user-notice', 'after-allow', 'B'); await details(empty);
    check('empty_history_does_not_claim_unchanged_configuration', (await empty.notices.innerText()).includes('空记录不表示配置未变化') && (await empty.notices.innerText()).includes('当前查询通知 0 条'));
    const reopened = await open('policy-notice', 'reopened'); await details(reopened);
    check('reopened_history_is_saved_without_current_count', !(await reopened.summary.innerText()).includes('当前查询收到') && (await reopened.notices.innerText()).includes('已保存记录'));
    check('reopened_history_keeps_original_receipt_snapshot', (await reopened.notices.innerText()).includes(notice.receipt_snapshot_id));
    await capture(reopened, 'reopened-history-1440');
    const unknown = await open('policy-notice', 'after-allow', 'A', 'unknown'); await details(unknown);
    check('unknown_source_and_path_controls_are_readable', (await unknown.notices.innerText()).includes('future_中文\\u{A}\\u{202E}') && (await unknown.notices.innerText()).includes('/owned/path\\u{A}\\u{202E}.json'));
    await capture(unknown, 'unknown-source-1440');
    const optional = await open('policy-notice', 'after-allow', 'A', 'optional'); await details(optional);
    check('prototype_named_unknown_source_is_plain_text', (await optional.notices.innerText()).includes('未知来源（__proto__）'));
    check('missing_file_path_is_not_inferred', (await optional.notices.innerText()).includes('原生通知未报告文件路径'));
    const gap = await open('policy-notice', 'after-allow', 'A', 'gap'); await details(gap);
    check('recording_gap_visible', (await gap.notices.innerText()).includes('部分配置通知未能保存或核对'));
    const unregistered = await open('policy-notice', 'after-allow', 'A', 'unregistered'); await details(unregistered);
    check('missing_registration_not_claimed_available', (await unregistered.notices.innerText()).includes('通知回调登记尚未确认'));
    const legacy = await open('policy-notice', 'after-allow', 'A', 'legacy'); await details(legacy);
    check('legacy_absence_is_not_an_empty_verified_history', (await legacy.notices.innerText()).includes('尚无可核对的配置通知记录'));
    const truncated = await open('policy-notice', 'after-allow', 'A', 'truncated'); await details(truncated);
    check('truncation_and_total_query_count_are_distinct', await truncated.notices.locator('.snapshot-asset').count() === 128 && (await truncated.notices.innerText()).includes('最近 128 条') && (await truncated.summary.innerText()).includes('131 条配置通知'));
    await sibling.tab.evaluate(() => window.dispatchEvent(new Event('offline')));
    await sibling.tab.waitForFunction(() => !document.querySelector('.execution-snapshot > summary')?.textContent.includes('当前查询收到'));
    check('disconnect_clears_current_claim_but_preserves_history', (await sibling.notices.innerText()).includes('已保存记录') && !(await sibling.notices.innerText()).includes('原生已确认当前查询'));
    check('only_three_explicit_original_refusals', commands.length === 3 && commands.every(c => c.body.kind === 'respond' && c.body.option_id === 'deny'));
    check('no_browser_errors', errors.length === 0); check('no_unmapped_requests', unmapped.length === 0);
    return {checks, commands, errors, unmapped, passed: Object.values(checks).every(Boolean)};
  } catch (error) {
    const last = pages.at(-1); if (last) await last.screenshot({path: output + '/failure.png'});
    return {checks, commands, errors, unmapped, passed: false, failure: String(error), last_page: last ? await last.locator('body').innerText() : ''};
  } finally {for (const tab of pages) await tab.close();}
}
