// Production HTTP frames from the four-profile native test. Missing metadata
// cases are explicit display fixtures; intercepted creates only check the form.
async page => {
  page = await page.context().newPage();
  const fixture = __FIXTURE__, assets = __ASSETS__, output = __OUTPUT__;
  const checks = {}, requests = [], errors = [], unmapped = [];
  const copy = value => JSON.parse(JSON.stringify(value));
  const check = (name, value) => { checks[name] = Boolean(value); if (!value) throw Error(name); };
  let frame = 'initial', display = 'native';
  page.setDefaultTimeout(8000);
  page.on('pageerror', error => errors.push(error.message));
  await page.addInitScript(() => {
    if (location.origin !== 'http://127.0.0.1:9469') return;
    sessionStorage.clear();
    sessionStorage.setItem('netcap.selected.v1', new URL(location.href).searchParams.get('session'));
    window.EventSource = class {
      constructor() { setTimeout(() => this.onopen?.(), 0); }
      addEventListener() {}
      close() {}
    };
  });
  const contract = snapshot => {
    const selection = snapshot?.mcp_selections?.find(row => row.asset_id === 'shared');
    if (!selection?.definition || display === 'native') return;
    if (display === 'legacy') delete selection.definition;
    if (display === 'missing') delete selection.definition.environment;
    if (display === 'unknown') selection.definition.version = 99;
    if (display === 'disabled-unused') { selection.mode = 'disabled'; selection.definition.environment = []; }
    if (display === 'no-references') selection.definition.environment = [];
    if (display === 'fallback') selection.definition.environment[0].present = false;
  };
  await page.route('**/*', async route => {
    const request = route.request(), path = request.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    requests.push({path, method: request.method(), body: request.postDataJSON()});
    if (path === '/api/commands') return route.fulfill({status: 409, json: {error: '表单展示验收：没有创建新执行。'}});
    if (fixture.frames[frame][path]) {
      const value = copy(fixture.frames[frame][path]);
      contract(value.snapshot);
      contract(value.session?.execution_snapshot);
      return route.fulfill({json: value});
    }
    if (assets[path]) return route.fulfill({path: assets[path], contentType: path.endsWith('.js') ? 'application/javascript' : path.endsWith('.css') ? 'text/css' : path.endsWith('.svg') ? 'image/svg+xml' : 'text/html'});
    if (path.startsWith('/favicon.')) return route.fulfill({status: 204});
    unmapped.push(path);
    return route.fulfill({status: 404, body: 'Unmapped shared MCP acceptance request'});
  });
  const details = page.locator('.execution-snapshot');
  const definition = details.locator(':scope > .mcp-selection [data-mcp-definition="shared"]');
  const openDetails = async () => {
    await details.waitFor({state: 'attached'});
    if (!await details.isVisible()) await page.getByRole('button', {name: '详情', exact: true}).click();
    if (await details.getAttribute('open') === null) await details.locator(':scope > summary').click();
  };
  const load = async (nextFrame, profile = 'codex-a', mode = 'native') => {
    frame = nextFrame; display = mode;
    await page.goto('http://127.0.0.1:9469/?session=' + fixture.sessions[profile]);
    await openDetails();
  };
  const openDefinition = async () => {
    await definition.waitFor({state: 'attached'});
    if (await definition.getAttribute('open') === null) await definition.locator(':scope > summary').click();
  };
  const screenshot = async label => {
    await definition.evaluate(element => {
      for (let parent = element.parentElement; parent; parent = parent.parentElement) {
        if (parent.scrollHeight > parent.clientHeight && /auto|scroll/.test(getComputedStyle(parent).overflowY)) {
          parent.scrollTop += element.getBoundingClientRect().top - parent.getBoundingClientRect().top - 24;
        }
      }
    });
    await page.screenshot({path: output + '/' + label + '.png'});
  };
  try {
    await page.setViewportSize({width: 1440, height: 1100});
    await load('initial');
    for (const profile of Object.keys(fixture.sessions)) {
      await page.getByRole('button', {name: '＋ 新建', exact: true}).click();
      const form = page.getByRole('dialog', {name: '新建会话', exact: true});
      await form.locator('select[name="route"]').selectOption(profile + '-route');
      const choice = form.locator('select[name="capability_selection:shared"]');
      check(profile + '_shared_definition_can_be_selected', await choice.count() === 1);
      check(profile + '_shared_choice_defaults_to_inherit', await choice.inputValue() === 'inherit');
      check(profile + '_reference_remains_independent', !await form.locator('input[name="capability_ref"][value="shared"]').isChecked());
      check(profile + '_private_backend_asset_can_be_selected', await form.locator('select[name="capability_selection:private-' + profile + '"]').count() === 1);
      const mode = profile.endsWith('-a') ? 'enabled' : 'disabled';
      await choice.selectOption(mode);
      const response = page.waitForResponse(row => row.url().endsWith('/api/commands'));
      await form.getByRole('button', {name: '创建会话', exact: true}).click();
      const body = (await response).request().postDataJSON();
      check(profile + '_explicit_selection_payload', body.kind === 'create' && body.route_id === profile + '-route' && body.capability_refs.length === 0 &&
        JSON.stringify(body.capability_selections) === JSON.stringify([{asset_id: 'shared', mode}]));
      await form.getByRole('button', {name: '关闭新建会话', exact: true}).click();
    }
    for (const profile of ['codex-a', 'codex-b']) {
      await load('initial', profile);
      await openDefinition();
      const text = await definition.innerText();
      const selection = fixture.frames.initial['/api/sessions/' + fixture.sessions[profile] + '/execution'].snapshot.mcp_selections.find(row => row.asset_id === 'shared');
      check(profile + '_source_version_and_environment_names_visible', text.includes(selection.source_path) && text.includes(selection.definition.sha256) &&
        selection.definition.environment.every(binding => text.includes(binding.name + ' · 记录时已设置')));
      check(profile + '_does_not_claim_loaded_definition_version', text.includes('不能独立证明加载了哪一版定义'));
      check(profile + '_credential_values_are_not_rendered', !text.includes('fixture-mcp-'));
      const change = page.locator('.execution-change');
      check(profile + '_change_only_offers_enable_and_disable', JSON.stringify(await change.locator('select[data-mcp-mode="shared"] option').evaluateAll(rows => rows.map(row => row.value))) === JSON.stringify(['enabled', 'disabled']));
      check(profile + '_unsupported_resource_changes_are_not_offered', await change.getByLabel('添加 MCP 资产', {exact: true}).count() === 0 &&
        await change.locator('select[data-mcp-version="shared"]').count() === 0 && (await change.innerText()).includes('新增、恢复继承和替换资源版本尚未开放'));
    }
    await screenshot('native-1440');
    await page.setViewportSize({width: 390, height: 844});
    await openDetails();
    check('mobile_has_no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
    await screenshot('native-390');
    await load('shared-disabled');
    await openDefinition();
    check('disabled_binding_is_saved_but_unused', (await definition.innerText()).includes('保留先前环境引用记录') && (await definition.innerText()).includes('本版本不使用'));
    const historical = page.getByRole('region', {name: '历史执行快照'}).locator('details').first();
    await historical.locator(':scope > summary').click();
    const oldDefinition = historical.locator('[data-mcp-definition="shared"]');
    await oldDefinition.locator(':scope > summary').click();
    check('history_retains_original_definition_binding', (await oldDefinition.innerText()).includes('已绑定所选 Codex 后台的环境引用'));
    await screenshot('disabled-390');
    await load('reenabled-used');
    await openDefinition();
    check('reenabled_snapshot_shows_active_binding', (await definition.innerText()).includes('输入前核对其版本') && !(await definition.innerText()).includes('本版本不使用'));
    await page.setViewportSize({width: 1440, height: 1100});
    await load('reenabled-used', 'claude-a');
    check('claude_does_not_invent_codex_environment_metadata', await definition.count() === 0);
    check('claude_keeps_supported_resource_change_options', await page.locator('select[data-mcp-mode="shared"] option[value="inherit"]').count() === 1);
    for (const mode of ['legacy', 'missing', 'unknown', 'disabled-unused', 'no-references', 'fallback']) {
      await load('initial', 'codex-a', mode);
      if (mode === 'legacy') { check('legacy_does_not_invent_environment_binding', await definition.count() === 0); continue; }
      await openDefinition();
      const text = await definition.innerText();
      if (mode === 'missing' || mode === 'unknown') check(mode + '_binding_gap_is_visible', text.includes('环境引用版本尚未核对') && !text.includes('已绑定所选'));
      if (mode === 'disabled-unused') check('unused_disabled_definition_does_not_claim_credentials_read', text.includes('未读取环境引用；再次启用时核对当前配置'));
      if (mode === 'no-references') check('empty_binding_has_no_credential_requirement', text.includes('此定义未引用后台环境变量'));
      if (mode === 'fallback') check('absent_reference_is_distinct_from_present', text.includes('记录时未设置，采用定义默认值'));
    }
    check('only_form_payload_commands_were_intercepted', requests.filter(row => row.path === '/api/commands').length === 4 && requests.filter(row => row.path === '/api/commands').every(row => row.body.kind === 'create'));
    check('all_http_requests_mapped', unmapped.length === 0);
    check('no_browser_errors', errors.length === 0);
    return {passed: true, checks, requests, errors, scope: 'four-profile native Application/Handler frame replay; intercepted create forms and explicit synthetic metadata boundaries'};
  } catch (error) {
    await page.screenshot({path: output + '/failure.png'});
    return {passed: false, checks, requests, errors, unmapped, error: String(error)};
  } finally {
    await page.close();
  }
}
