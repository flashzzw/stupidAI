// Production assets replay real Application/Handler/CLI catalog DTOs.
// Unknown, absent and empty reports are explicit display-contract fixtures.
async page => {
  page = await page.context().newPage();
  const fixture = __FIXTURE__, assets = __ASSETS__, output = __OUTPUT__;
  const checks = {}, requests = [], errors = [], unmapped = [];
  const sid = fixture.sessions.a, other = fixture.sessions.b;
  const copy = value => JSON.parse(JSON.stringify(value));
  const check = (name, value) => { checks[name] = Boolean(value); if (!value) throw Error(name); };
  const base = '/api/sessions/' + sid;
  const sample = fixture.responses[base + '/skills/catalog'].instructions;
  let mode = 'native';
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
    if (path.endsWith('/skills/catalog')) {
      if (mode === 'failed') return route.fulfill({status: 409, json: {error: '原生查询未开启；读取清单不会启动或恢复查询'}});
      const value = copy(fixture.responses[path]);
      if (mode === 'missing') delete value.instructions;
      if (mode === 'unavailable') value.instructions = {available: false, items: []};
      if (mode === 'empty') value.instructions = {available: true, items: []};
      if (mode === 'unknown') value.instructions.items.push({path: '/contract/unknown/CLAUDE.md', source: 'FutureNativeSource', tokens: 0});
      return route.fulfill({json: value});
    }
    if (fixture.responses[path]) return route.fulfill({json: fixture.responses[path]});
    if (assets[path]) return route.fulfill({path: assets[path], contentType: path.endsWith('.js') ? 'application/javascript' : path.endsWith('.css') ? 'text/css' : path.endsWith('.svg') ? 'image/svg+xml' : 'text/html'});
    if (path.startsWith('/favicon.')) return route.fulfill({status: 204});
    unmapped.push(path);
    return route.fulfill({status: 404, body: 'Unmapped instruction source acceptance request'});
  });
  const panel = page.locator('details').filter({has: page.locator(':scope > summary').getByText('Claude Skill 来源清单', {exact: true})});
  const region = page.getByRole('region', {name: '当前查询的指令文件', exact: true});
  const openPanel = async () => {
    const details = page.locator('.execution-snapshot');
    await details.waitFor({state: 'attached'});
    if (!await details.isVisible()) await page.getByRole('button', {name: '详情', exact: true}).click();
    if (await details.getAttribute('open') === null) await details.locator(':scope > summary').click();
    if (await panel.getAttribute('open') === null) await panel.locator(':scope > summary').click();
  };
  const read = async value => {
    mode = value;
    await panel.getByRole('button', {name: '读取当前原生清单', exact: true}).click();
    await panel.getByRole(value === 'failed' ? 'alert' : 'status').waitFor();
  };
  const screenshot = async label => {
    await region.evaluate(element => {
      for (let parent = element.parentElement; parent; parent = parent.parentElement) {
        if (parent.scrollHeight > parent.clientHeight && /auto|scroll/.test(getComputedStyle(parent).overflowY)) {
          parent.scrollTop += element.getBoundingClientRect().top - parent.getBoundingClientRect().top - 24;
        }
      }
    });
    await page.screenshot({path: output + '/' + label + '.png'});
  };
  const noOverflow = async label => check(label + '_no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
  try {
    await page.setViewportSize({width: 1440, height: 1100});
    await page.goto('http://127.0.0.1:9468/');
    await openPanel();
    check('no_catalog_request_before_explicit_read', requests.every(request => !request.path.endsWith('/skills/catalog')));
    await read('native');
    const text = await region.innerText();
    check('all_five_owned_instruction_paths_visible', fixture.files.length === 5 && fixture.files.every(path => text.includes(path)));
    check('native_source_count_preserved', await region.locator('.snapshot-asset').count() === sample.items.length);
    check('user_and_project_source_labels_visible', text.includes('来源：用户') && text.includes('来源：项目（含上层目录）'));
    check('native_token_estimates_visible', sample.items.every(item => text.includes('约 ' + item.tokens.toLocaleString() + ' tokens')));
    check('instruction_body_not_rendered', !text.includes('PRIVATE_INSTRUCTION_BODY_'));
    check('loaded_version_not_claimed', text.includes('未绑定已加载的文件版本'));
    check('nested_and_historical_source_limits_visible', text.includes('工具触发的嵌套指令及历史旧内容'));
    check('source_and_permission_limits_visible', text.includes('完整来源、独立排除与权限覆盖仍需另外核对'));
    check('first_query_identity_visible', (await panel.innerText()).includes(fixture.responses[base].session.scope.native_id));
    await noOverflow('desktop');
    await screenshot('native-1440');
    await page.setViewportSize({width: 390, height: 844});
    await openPanel();
    await noOverflow('mobile');
    await screenshot('native-390');
    for (const value of ['missing', 'unavailable', 'empty']) {
      await read(value);
      const text = await region.innerText();
      check(value + '_does_not_retain_previous_paths', await region.locator('.snapshot-asset').count() === 0);
      check(value + '_has_accurate_coverage', value === 'empty'
        ? text.includes('本次报告未列出指令文件') && !text.includes('未提供可核对')
        : text.includes('未提供可核对的指令文件清单') && !text.includes('原生报告了 0'));
    }
    await read('unknown');
    const unknown = region.locator('.snapshot-asset').filter({hasText: '/contract/unknown/CLAUDE.md'});
    check('unknown_native_source_and_zero_estimate_preserved', (await unknown.innerText()).includes('FutureNativeSource · 约 0 tokens'));
    await screenshot('unknown-390');
    await read('failed');
    check('failed_read_clears_instruction_region', await region.count() === 0);
    check('closed_query_reason_visible', (await panel.innerText()).includes('不会启动或恢复查询'));
    await read('native');
    await page.setViewportSize({width: 1440, height: 1100});
    await page.locator('.session-row').filter({hasText: 'Instruction sources B'}).click();
    await openPanel();
    check('session_switch_clears_instruction_region', await region.count() === 0);
    await read('native');
    check('second_query_identity_visible', (await panel.innerText()).includes(fixture.responses['/api/sessions/' + other].session.scope.native_id));
    await page.evaluate(() => window.dispatchEvent(new Event('offline')));
    await panel.getByText('工作台已断开，连接后可重新读取。', {exact: true}).waitFor({state: 'attached'});
    await openPanel();
    check('disconnect_clears_instruction_region', await region.count() === 0);
    check('disconnect_disables_read', await panel.getByRole('button', {name: '读取当前原生清单', exact: true}).isDisabled());
    const reads = requests.filter(request => request.path.endsWith('/skills/catalog'));
    check('catalog_uses_existing_empty_body_request', reads.length === 8 && reads.every(request => request.method === 'POST' && JSON.stringify(request.body) === '{}'));
    check('no_execution_commands', requests.every(request => request.path !== '/api/commands'));
    check('all_http_requests_mapped', unmapped.length === 0);
    check('no_browser_errors', errors.length === 0);
    return {passed: true, checks, requests, errors, scope: 'real native Application DTOs for two queries; controlled HTTP and missing/unavailable/empty/unknown contracts'};
  } finally {
    await page.close();
  }
}
