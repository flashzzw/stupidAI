// Production assets replay captured Application/Handler/CLI reports. Synthetic
// empty, unknown and conflicting reports exercise only the display contract.
async page => {
  page = await page.context().newPage();
  const fixture = __FIXTURE__, assets = __ASSETS__, output = __OUTPUT__;
  const checks = {}, requests = [], errors = [], unmapped = [];
  const sid = fixture.sessions.A, other = fixture.sessions.B;
  const base = '/api/sessions/' + sid, otherBase = '/api/sessions/' + other;
  const copy = value => JSON.parse(JSON.stringify(value));
  const check = (name, value) => { checks[name] = Boolean(value); if (!value) throw Error(name); };
  const initial = fixture.frames.initial[base + '/permissions/catalog'];
  const closed = fixture.http.find(row => row.status === 409 && row.path === base + '/permissions/catalog').response;
  let mode = 'initial', releaseLate, held;
  page.setDefaultTimeout(8000);
  page.on('pageerror', error => errors.push(error.message));
  await page.addInitScript(id => {
    if (location.origin !== 'http://127.0.0.1:9468') return;
    sessionStorage.clear();
    sessionStorage.setItem('netcap.selected.v1', id);
    window.EventSource = class {
      constructor() { setTimeout(() => this.onopen?.(), 0); }
      addEventListener() {}
      close() {}
    };
  }, sid);
  await page.route('**/*', async route => {
    const request = route.request(), path = request.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    requests.push({path, method: request.method(), body: request.postData() ? JSON.parse(request.postData()) : null});
    if (path.endsWith('/permissions/catalog')) {
      if (mode === 'failed') return route.fulfill({status: 409, json: closed});
      if (mode === 'late') {
        held?.();
        return new Promise(resolve => { releaseLate = async () => { await route.fulfill({json: initial}); resolve(); }; });
      }
      if (['initial', 'managed', 'ask', 'invalid', 'resumed'].includes(mode)) return route.fulfill({json: fixture.frames[mode][path]});
      const value = copy(fixture.frames.initial[path]);
      if (mode === 'repeat') return route.fulfill({json: {...value, at: fixture.reports['repeated-A'].at}});
      if (mode === 'empty') { value.items = []; value.directories = []; value.errors = []; value.conflict_count = 0; }
      if (mode === 'unknown') value.items.push({behavior: 'ask', source: 'FuturePolicySource', rule: 'Bash(echo 中文🦊\n\u202E<safe>\\u{A})', editability: 'readonly', not_in_effect: false, conflicting: false});
      if (mode === 'conflict') {
        const row = copy(value.items.find(row => row.source === 'userSettings'));
        value.items = [{...row, not_in_effect: false, conflicting: true}, {...row, not_in_effect: true, conflicting: true}];
        value.managed_only = true; value.conflict_count = 1;
      }
      value.sample_hash = mode.padEnd(64, '0');
      return route.fulfill({json: value});
    }
    if (fixture.frames.initial[path]) return route.fulfill({json: fixture.frames.initial[path]});
    if (assets[path]) return route.fulfill({path: assets[path], contentType: path.endsWith('.js') ? 'application/javascript' : path.endsWith('.css') ? 'text/css' : path.endsWith('.svg') ? 'image/svg+xml' : 'text/html'});
    if (path.startsWith('/favicon.')) return route.fulfill({status: 204});
    unmapped.push(path);
    return route.fulfill({status: 404, body: 'Unmapped permission acceptance request'});
  });
  const panel = page.locator('.permission-catalog');
  const openPanel = async () => {
    const details = page.locator('.execution-snapshot');
    await details.waitFor({state: 'attached'});
    if (!await details.isVisible()) await page.getByRole('button', {name: '详情', exact: true}).click();
    if (await details.getAttribute('open') === null) await details.locator(':scope > summary').click();
    if (await panel.getAttribute('open') === null) await panel.locator(':scope > summary').click();
  };
  const read = async value => {
    mode = value;
    const response = page.waitForResponse(response => response.url().endsWith('/permissions/catalog'));
    await panel.getByRole('button', {name: '读取当前权限规则', exact: true}).click();
    await response;
    await panel.getByRole('button', {name: '读取当前权限规则', exact: true}).waitFor();
    await panel.getByRole(value === 'failed' ? 'alert' : 'status').waitFor();
  };
  const screenshot = async label => {
    await panel.evaluate(element => {
      for (let parent = element.parentElement; parent; parent = parent.parentElement) {
        if (parent.scrollHeight > parent.clientHeight && /auto|scroll/.test(getComputedStyle(parent).overflowY))
          parent.scrollTop += element.getBoundingClientRect().top - parent.getBoundingClientRect().top - 24;
      }
    });
    await page.screenshot({path: output + '/' + label + '.png'});
  };
  const noOverflow = async label => check(label + '_no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
  try {
    await page.setViewportSize({width: 1440, height: 1100});
    await page.goto('http://127.0.0.1:9468/');
    await openPanel();
    check('read_is_explicit', requests.every(row => !row.path.endsWith('/permissions/catalog')));
    await read('initial');
    check('original_query_identity_visible', (await panel.innerText()).includes(initial.native_id));
    check('initial_is_a_comparison_baseline', (await panel.innerText()).includes('本次查询的首次读取'));
    const sourceOptions = await panel.getByLabel('按权限来源查看').locator('option').allTextContents();
    check('five_native_settings_sources_visible', ['用户设置', '项目设置', '项目本地设置', '当前查询设置', '管理员受管'].every(label => sourceOptions.includes(label)));
    check('full_permission_not_claimed', (await panel.innerText()).includes('完整权限仍需核对'));
    await panel.getByLabel('按权限来源查看').selectOption('userSettings');
    check('source_filter_preserves_native_rules', await panel.locator('.snapshot-asset').count() === initial.items.filter(row => row.source === 'userSettings').length);
    check('stored_rule_spelling_visible', (await panel.innerText()).includes('Read(./target-A.txt)'));
    await noOverflow('desktop');
    await screenshot('sources-1440');
    await read('repeat');
    check('same_report_comparison', (await panel.innerText()).includes('与本页上次读取一致'));
    await read('managed');
    check('native_hot_change_visible', (await panel.innerText()).includes('权限规则报告已变化'));
    check('managed_only_visible', (await panel.innerText()).includes('仅采用受管设置中的权限规则'));
    await panel.getByLabel('按权限来源查看').selectOption('userSettings');
    check('native_ignored_rule_preserved', (await panel.innerText()).includes('原生标记：不生效') && (await panel.innerText()).includes('Read(./target-A.txt)'));
    await page.setViewportSize({width: 390, height: 844});
    await openPanel();
    await noOverflow('mobile');
    await screenshot('managed-390');
    await read('ask');
    await panel.getByLabel('按权限来源查看').selectOption('policySettings');
    check('native_ask_rule_visible', (await panel.innerText()).includes('询问规则 · 管理员受管'));
    await read('invalid');
    check('parse_error_is_visible_without_private_value', (await panel.innerText()).includes('配置解析错误') && !(await panel.innerText()).includes('PRIVATE_INVALID_VALUE'));
    await read('resumed');
    check('new_native_query_resets_comparison', (await panel.innerText()).includes('原生查询或配置已变化'));
    await read('empty');
    check('empty_is_not_missing_or_unrestricted', (await panel.innerText()).includes('本次报告没有列出规则') && (await panel.innerText()).includes('不代表所有动作均被允许') && await panel.locator('.snapshot-asset').count() === 0);
    await read('unknown');
    await panel.getByLabel('按权限来源查看').selectOption('FuturePolicySource');
    const unknown = await panel.locator('.snapshot-asset').innerText();
    check('unknown_source_preserved', unknown.includes('未知来源（FuturePolicySource）'));
    check('control_and_bidi_characters_escaped', unknown.includes('\\u{A}') && unknown.includes('\\u{202E}') && !unknown.includes('\u202E'));
    check('literal_backslash_and_markup_remain_text', unknown.includes('\\\\u{A}') && unknown.includes('<safe>') && await panel.locator('safe').count() === 0);
    await noOverflow('unknown');
    await screenshot('unknown-390');
    await read('conflict');
    check('native_conflicting_rows_not_merged', await panel.locator('.snapshot-asset').count() === 2);
    check('conflicting_state_visible', (await panel.innerText()).includes('同时出现不同的忽略标记'));
    await screenshot('conflict-390');
    await read('failed');
    check('failed_read_clears_old_report', await panel.getByRole('status').count() === 0 && await panel.locator('.snapshot-asset').count() === 0);
    check('closed_query_error_visible', (await panel.innerText()).includes(closed.error));
    await read('initial');
    check('failure_gap_resets_comparison', (await panel.innerText()).includes('本次查询的首次读取'));
    await page.setViewportSize({width: 1440, height: 1100});
    await openPanel();
    mode = 'late';
    const waiting = new Promise(resolve => { held = resolve; });
    await panel.getByRole('button', {name: '读取当前权限规则', exact: true}).click();
    await waiting;
    await page.locator('.session-row').filter({hasText: 'Permission rules B'}).click();
    await openPanel();
    await releaseLate(); releaseLate = undefined;
    check('late_other_query_report_discarded', await panel.getByRole('status').count() === 0 && !(await panel.innerText()).includes(initial.native_id));
    await read('initial');
    check('second_profile_identity_visible', (await panel.innerText()).includes(fixture.frames.initial[otherBase + '/permissions/catalog'].native_id));
    await page.evaluate(() => window.dispatchEvent(new Event('offline')));
    await panel.getByText('工作台已断开，连接后可重新读取。', {exact: true}).waitFor({state: 'attached'});
    await openPanel();
    check('disconnect_clears_old_evidence', await panel.getByRole('status').count() === 0);
    check('disconnect_disables_read', await panel.getByRole('button', {name: '读取当前权限规则', exact: true}).isDisabled());
    const reads = requests.filter(row => row.path.endsWith('/permissions/catalog'));
    check('only_empty_body_read_requests', reads.length >= 13 && reads.every(row => row.method === 'POST' && JSON.stringify(row.body) === '{}'));
    check('no_execution_commands', requests.every(row => row.path !== '/api/commands'));
    check('all_http_requests_mapped', unmapped.length === 0);
    check('no_browser_errors', errors.length === 0);
    return {passed: true, checks, requests, errors, scope: 'captured native Handler reports for two profiles and a resumed original ID; controlled empty, unknown, conflict and late-response display contracts'};
  } finally {
    if (releaseLate) await releaseLate();
    await page.close();
  }
}
