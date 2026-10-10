// Replay real mixed-profile HTTP frames and full-form preview responses.
// The browser does not send native commands or make model requests.
async page => {
  page = await page.context().newPage();
  const fixture = __FIXTURE__, assets = __ASSETS__, output = __OUTPUT__;
  const checks = {}, requests = [], errors = [], unmapped = [];
  const check = (name, value) => { checks[name] = Boolean(value); if (!value) throw Error(name); };
  const stable = value => JSON.stringify(value, (_, child) => child && !Array.isArray(child) && typeof child === 'object'
    ? Object.fromEntries(Object.keys(child).sort().map(key => [key, child[key]])) : child);
  let frame = 'initial', activeProfile = 'codex-a', plannedPreview = '', failure;
  page.setDefaultTimeout(8000);
  page.on('pageerror', error => errors.push(error.message));
  await page.addInitScript(() => {
    if (location.origin !== 'http://127.0.0.1:9470') return;
    sessionStorage.clear();
    sessionStorage.setItem('netcap.selected.v1', new URL(location.href).searchParams.get('session'));
    window.EventSource = class {
      constructor() { setTimeout(() => this.onopen?.(), 0); }
      addEventListener() {}
      close() {}
    };
  });
  await page.route('**/*', async route => {
    const request = route.request(), path = request.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    const body = request.postDataJSON();
    if (path.startsWith('/api/')) requests.push({path, method: request.method(), body});
    if (path.endsWith('/execution/preview')) {
      const key = plannedPreview + '/' + activeProfile, saved = fixture.form_previews[key];
      checks[key + '_browser_payload_matches_actual_handler_request'] = Boolean(saved && stable(body) === stable(saved.request));
      if (!checks[key + '_browser_payload_matches_actual_handler_request']) {
        unmapped.push({path, body, expected: saved?.request});
        return route.fulfill({status: 409, json: {error: 'Browser preview differs from the recorded production request.'}});
      }
      return route.fulfill({json: saved.preview});
    }
    if (fixture.frames[frame][path]) return route.fulfill({json: fixture.frames[frame][path]});
    if (assets[path]) return route.fulfill({path: assets[path], contentType: path.endsWith('.js') ? 'application/javascript' : path.endsWith('.css') ? 'text/css' : path.endsWith('.svg') ? 'image/svg+xml' : 'text/html'});
    if (path.startsWith('/favicon.')) return route.fulfill({status: 204});
    unmapped.push({path, body});
    return route.fulfill({status: 404, body: 'Unmapped mixed-capability acceptance request'});
  });
  const details = page.locator('.execution-snapshot');
  const skills = details.getByRole('region', {name: '本次原生 Skill 选择', exact: true});
  const plugins = details.getByRole('region', {name: '本次 CLI 插件选择', exact: true});
  const change = details.locator('.execution-change');
  const execution = (label, profile) => fixture.frames[label]['/api/sessions/' + fixture.sessions[profile] + '/execution'];
  const load = async (label, profile = 'codex-a') => {
    frame = label; activeProfile = profile; plannedPreview = '';
    await page.goto('http://127.0.0.1:9470/?session=' + fixture.sessions[profile]);
    await details.waitFor({state: 'attached'});
    if (!await details.isVisible()) await page.getByRole('button', {name: '详情', exact: true}).click();
    if (await details.getAttribute('open') === null) await details.locator(':scope > summary').click();
  };
  const screenshot = async (label, target) => {
    await target.evaluate(element => {
      for (let parent = element.parentElement; parent; parent = parent.parentElement) {
        if (parent.scrollHeight > parent.clientHeight && /auto|scroll/.test(getComputedStyle(parent).overflowY)) {
          parent.scrollTop += element.getBoundingClientRect().top - parent.getBoundingClientRect().top - 24;
        }
      }
    });
    await page.screenshot({path: output + '/' + label + '.png'});
  };
  const inspect = async (label, profile, expectedCount) => {
    plannedPreview = label;
    await change.getByRole('button', {name: '预览改配差异', exact: true}).click();
    const preview = change.locator(':scope > [role="status"]');
    await preview.waitFor({state: 'visible'});
    const lines = (await preview.locator('p').allTextContents()).filter(text => text.includes('→'));
    check(label + '_' + profile + '_only_real_changes_are_rendered', lines.length === expectedCount && lines.every(text => !text.includes('identity：') && !text.includes('exclusive：')));
    check(label + '_' + profile + '_preview_can_be_saved', await preview.getByRole('button', {name: '保存待应用选择', exact: true}).isEnabled());
    return preview;
  };
  try {
    await page.setViewportSize({width: 1440, height: 1100});
    for (const profile of Object.keys(fixture.sessions)) {
      await load('initial', profile);
      check(profile + '_shared_and_private_skills_have_one_panel', await skills.count() === 1 && await skills.locator(':scope > .snapshot-asset').count() === 2 &&
        (await skills.innerText()).includes('shared-method · 启用') && (await skills.innerText()).includes('method-' + profile + ' · 启用'));
      check(profile + '_cli_plugin_is_separate_from_skills', await plugins.count() === 1 && await plugins.locator(':scope > .snapshot-asset').count() === 1 &&
        (await plugins.innerText()).includes('suite-' + profile.split('-')[0] + ' · 启用整包'));
      const components = plugins.locator('details');
      await components.locator(':scope > summary').click();
      check(profile + '_plugin_shows_both_component_types', (await components.innerText()).includes('mcp · ') && (await components.innerText()).includes('skill · '));
      check(profile + '_five_independent_resource_choices', await change.locator('[data-mcp-mode]').count() === 2 && await change.locator('[data-skill-mode]').count() === 2 && await change.locator('[data-plugin-mode]').count() === 1);
      check(profile + '_method_gate_is_not_claimed', (await skills.innerText()).includes('Gate') && !(await details.innerText()).includes('fixture-mcp-'));
      await change.locator('[data-skill-mode="shared-method"]').selectOption('disabled');
      await change.locator('[data-plugin-mode="suite-' + profile.split('-')[0] + '"]').selectOption('disabled');
      await inspect('disabled', profile, 2);
      check(profile + '_preview_keeps_active_selection', (await skills.innerText()).includes('shared-method · 启用') && (await plugins.innerText()).includes('启用整包'));
    }
    await load('disabled-staged-codex-a');
    check('saved_candidate_is_not_active', (await details.locator(':scope > dl').innerText()).includes(execution(frame, activeProfile).snapshot.id) &&
      (await skills.innerText()).includes('shared-method · 启用') && (await change.innerText()).includes('待应用版本 2 · 尚未生效'));
    await screenshot('mixed-pending-1440', change);
    for (const profile of Object.keys(fixture.sessions)) {
      await load('disabled', profile);
      check(profile + '_combined_disable_keeps_private_skill', (await skills.innerText()).includes('shared-method · 禁用') && (await skills.innerText()).includes('method-' + profile + ' · 启用') && (await plugins.innerText()).includes('禁用整包'));
      const history = details.getByRole('region', {name: '历史执行快照', exact: true});
      await history.locator(':scope > details > summary').first().click();
      check(profile + '_original_version_is_visible_in_history', (await history.innerText()).includes('shared-method · 资源 v1') && (await history.innerText()).includes('identity · 启用'));
    }
    for (const [label, affected] of [['drift-shared-method', Object.keys(fixture.sessions)], ['drift-method-claude-a', ['claude-a']]]) {
      for (const profile of Object.keys(fixture.sessions)) {
        await load(label, profile);
        const source = details.locator('.execution-source-check');
        await source.getByRole('button', {name: '核对当前来源', exact: true}).click();
        await source.getByRole('status').waitFor();
        check(label + '_' + profile + '_source_impact_is_visible', (await source.innerText()).includes(affected.includes(profile) ? '来源或绑定已变化' : '已记录的来源与绑定未发现变化'));
        check(label + '_' + profile + '_file_check_does_not_claim_method_use', (await source.innerText()).includes('不确认当前加载、输入权、方法使用或 Gate 结果'));
      }
    }
    for (const profile of Object.keys(fixture.sessions)) {
      await load('registered-v2', profile);
      check(profile + '_new_catalog_version_is_opt_in', await change.locator('[data-skill-version="shared-method"]').inputValue() === 'v1' &&
        (await details.locator(':scope > .snapshot-asset').allTextContents()).some(text => text.startsWith('shared-method · v1')));
    }
    for (const [label, profile] of [['registered-v2', 'codex-a'], ['v2-codex-a', 'claude-b']]) {
      await load(label, profile);
      await change.locator('[data-skill-version="shared-method"]').selectOption('v2');
      const preview = await inspect('v2', profile, 1);
      check(profile + '_explicit_version_difference_is_visible', (await preview.innerText()).includes('资源 v1 → v2'));
      await load('v2-staged-' + profile, profile);
      check(profile + '_version_candidate_keeps_active_v1', (await details.locator(':scope > .snapshot-asset').allTextContents()).some(text => text.startsWith('shared-method · v1')) &&
        (await change.innerText()).includes('资源 v1 → v2') && (await change.innerText()).includes('尚未生效'));
    }
    for (const profile of Object.keys(fixture.sessions)) {
      await load('versions-restored', profile);
      const version = ['codex-a', 'claude-b'].includes(profile) ? 'v2' : 'v1';
      check(profile + '_restart_shows_its_selected_version', (await details.locator(':scope > .snapshot-asset').allTextContents()).some(text => text.startsWith('shared-method · ' + version)));
      check(profile + '_restored_view_has_no_pending_candidate', !(await change.innerText()).includes('待应用版本'));
    }
    await load('versions-restored', 'claude-b');
    await screenshot('claude-mixed-1440', skills);
    await page.setViewportSize({width: 390, height: 844});
    await load('versions-restored', 'codex-a');
    check('mobile_has_no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
    await screenshot('codex-mixed-390', skills);
    check('browser_has_no_errors_or_unmapped_requests', errors.length === 0 && unmapped.length === 0);
    check('browser_never_dispatches_native_commands', requests.every(request => request.path !== '/api/commands'));
  } catch (error) {
    failure = String(error?.stack ?? error);
    await page.screenshot({path: output + '/failure.png'}).catch(() => {});
  } finally {
    await page.close();
  }
  return {passed: !failure && Object.values(checks).every(Boolean), checks, errors, unmapped, requests, failure};
}
