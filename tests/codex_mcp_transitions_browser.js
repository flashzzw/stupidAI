// Actual Application/Handler snapshots; browser commands are intercepted and
// checked against the corresponding native experiment, without another input.
async page => {
  page = await page.context().newPage();
  const fixture = __FIXTURE__, assets = __ASSETS__, output = __OUTPUT__;
  const checks = {}, requests = [], errors = [], unmapped = [];
  let phase = 'initial', selected = 'codex-a', previewTag = 'added';
  const check = (name, value) => { checks[name] = Boolean(value); if (!value) throw Error(name); };
  page.setDefaultTimeout(8000);
  page.on('pageerror', error => errors.push(error.message));
  await page.addInitScript(() => {
    sessionStorage.clear();
    sessionStorage.setItem('netcap.selected.v1', location.href.split('session=')[1]?.split('&')[0] ?? '');
    window.EventSource = class { constructor() { setTimeout(() => this.onopen?.(), 0); } addEventListener() {} close() {} };
  });
  await page.route('**/*', async route => {
    const request = route.request(), path = request.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    requests.push({path, method: request.method(), body: request.postDataJSON()});
    if (path.endsWith('/execution/preview')) return route.fulfill({json: fixture.transitions[previewTag].preview});
    if (path === '/api/commands') return route.fulfill({status: 409, json: {error: '界面请求已记录；本次浏览器验收不发送原生操作。'}});
    if (fixture.frames[phase][path]) return route.fulfill({json: fixture.frames[phase][path]});
    if (assets[path]) return route.fulfill({path: assets[path], contentType: path.endsWith('.js') ? 'application/javascript' : path.endsWith('.css') ? 'text/css' : path.endsWith('.svg') ? 'image/svg+xml' : 'text/html'});
    if (path.startsWith('/favicon.')) return route.fulfill({status: 204});
    unmapped.push(path);
    return route.fulfill({status: 404, body: 'Unmapped native MCP transition display request'});
  });
  const details = page.locator('.execution-snapshot');
  const load = async (next, label = 'codex-a') => {
    phase = next; selected = label;
    await page.goto('http://127.0.0.1:9469/?session=' + fixture.sessions[label]);
    await details.waitFor({state: 'attached'});
    if (!await details.isVisible()) await page.getByRole('button', {name: '详情', exact: true}).click();
    if (await details.getAttribute('open') === null) await details.locator(':scope > summary').click();
  };
  const definition = () => details.locator(':scope > .mcp-selection [data-mcp-definition="shared"]');
  const openDefinition = async () => {
    const node = definition();
    if (await node.getAttribute('open') === null) await node.locator(':scope > summary').click();
    return node.innerText();
  };
  const snapshot = (label = selected) => fixture.frames[phase]['/api/sessions/' + fixture.sessions[label] + '/execution'].snapshot;
  const screenshot = async name => {
    await page.screenshot({path: output + '/' + name + '.png'});
  };
  try {
    await page.setViewportSize({width: 1440, height: 1100});
    await load('initial');
    const initial = snapshot().mcp_selections.find(row => row.asset_id === 'shared').definition.binding;
    const text = await openDefinition();
    check('planned_launch_binding_visible', text.includes(initial.digest));
    check('planned_resource_version_visible', text.includes('v1'));
    check('new_mcp_can_be_selected', await details.getByRole('combobox', {name: '添加 MCP 资产', exact: true}).count() === 1);
    await details.getByRole('combobox', {name: '添加 MCP 资产', exact: true}).selectOption('added');
    await details.getByRole('button', {name: '预览改配差异', exact: true}).click();
    await details.getByRole('button', {name: '保存待应用选择', exact: true}).waitFor();
    let preview = requests.findLast(row => row.path.endsWith('/execution/preview')).body;
    check('addition_uses_explicit_resource_version', preview.selections.some(row => row.asset_id === 'added' && row.version === 'v1' && row.mode === 'enabled'));
    check('addition_preserves_current_choices', preview.selections.some(row => row.asset_id === 'shared' && row.mode === 'enabled' && row.version === 'v1'));
    await details.getByRole('button', {name: '保存待应用选择', exact: true}).click();
    const added = requests.findLast(row => row.path === '/api/commands').body;
    check('addition_save_binds_native_preview', added.kind === 'stage_execution' && added.execution_change.preview_hash === fixture.transitions.added.preview.preview_hash);

    previewTag = 'version';
    await load('v2_registered');
    await details.locator('select[data-mcp-version="shared"]').selectOption('v2');
    await details.getByRole('button', {name: '预览改配差异', exact: true}).click();
    await details.getByRole('button', {name: '保存待应用选择', exact: true}).waitFor();
    preview = requests.findLast(row => row.path.endsWith('/execution/preview')).body;
    check('upgrade_is_explicit', preview.selections.some(row => row.asset_id === 'shared' && row.version === 'v2'));
    check('upgrade_preserves_other_definition', preview.selections.some(row => row.asset_id === 'private-codex-a' && row.version === 'v1'));
    check('upgrade_diff_names_both_versions', (await details.innerText()).includes('v1 → v2'));

    await load('v2');
    await openDefinition();
    check('v2_actual_loading_is_visible', await details.locator('[data-mcp-evidence="identity"][data-mcp-phase="enabled"][data-mcp-version="v2"]').count() === 1);
    check('v1_call_history_stays_separate', await details.locator('[data-mcp-evidence="identity"][data-mcp-phase="history"][data-mcp-version="v1"]').count() === 1);
    check('actual_and_planned_labels_are_distinct', (await details.innerText()).includes('实际启动标识') && (await openDefinition()).includes('计划启动标识'));
    await details.locator('[data-mcp-evidence="identity"][data-mcp-phase="history"]').scrollIntoViewIfNeeded();
    await screenshot('versions-1440');
    await page.setViewportSize({width: 390, height: 844});
    if (!await details.isVisible()) await page.getByRole('button', {name: '详情', exact: true}).click();
    await details.locator('[data-mcp-evidence="identity"][data-mcp-phase="history"]').scrollIntoViewIfNeeded();
    await screenshot('versions-390');
    check('narrow_view_has_no_document_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));

    previewTag = 'inherit';
    await details.locator('select[data-mcp-mode="shared"]').selectOption('inherit');
    await details.locator('select[data-mcp-mode="added"]').selectOption('inherit');
    await details.getByRole('button', {name: '预览改配差异', exact: true}).click();
    await details.getByRole('button', {name: '保存待应用选择', exact: true}).waitFor();
    preview = requests.findLast(row => row.path.endsWith('/execution/preview')).body;
    check('inherit_releases_only_named_resources', ['shared', 'added'].every(id => preview.selections.some(row => row.asset_id === id && row.mode === 'inherit' && row.version === undefined)));
    check('inherit_preserves_private_binding', preview.selections.some(row => row.asset_id === 'private-codex-a' && row.mode === 'enabled' && row.version === 'v1'));

    for (const state of ['stale', 'restarted_stale']) {
      await load(state);
      const value = snapshot().mcp_selections.find(row => row.asset_id === 'shared').definition.binding;
      check(state + '_active_version_remains_v2', value.asset_version === 'v2' && (await openDefinition()).includes(value.digest));
      check(state + '_candidate_v3_visible', (await details.locator('.execution-change').innerText()).includes('v3'));
      check(state + '_actual_old_loading_not_claimed_matching', (await details.locator('[data-mcp-evidence="identity"][data-mcp-phase="enabled"]').innerText()).includes('未核对一致'));
      check(state + '_no_repeat_apply', await details.getByRole('button', {name: '应用已保存配置', exact: true}).count() === 0);
    }
    await screenshot('unknown-390');
    await load('inherit');
    check('released_definition_absent_from_active_selection', await definition().count() === 0);
    check('released_v1_calls_preserved', await details.locator('[data-mcp-evidence="identity"][data-mcp-phase="history"][data-mcp-version="v1"]').count() === 1);
    check('released_v2_calls_preserved', await details.locator('[data-mcp-evidence="identity"][data-mcp-phase="history"][data-mcp-version="v2"]').count() === 1);
    await load('final', 'last');
    check('empty_target_has_no_active_definition', !snapshot().mcp_selections?.length);
    check('inherited_service_visible', await details.locator('[data-mcp-evidence="keep"][data-mcp-phase="inherit"]').count() === 1);
    check('browser_never_sends_model_or_native_apply', requests.filter(row => row.path === '/api/commands').every(row => row.body.kind === 'stage_execution'));
    check('all_requests_mapped', unmapped.length === 0);
    check('no_page_errors', errors.length === 0);
    return {passed: true, checks, requests, errors, unmapped};
  } finally { await page.close(); }
}
