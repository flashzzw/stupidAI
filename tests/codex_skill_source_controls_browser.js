// Production Handler responses from TestCodexNativeSkillSourceControls.
// HTTP delivery, failures and connection timing are controlled; no live listener.
async page => {
  await page.unrouteAll({ behavior: 'ignoreErrors' });
  const fixture = __FIXTURE__, assets = __ASSETS__, checks = {}, errors = [], requests = [];
  page.__sourceControls = { checks, errors, requests };
  const check = (name, ok) => { checks[name] = !!ok; if (!ok) throw new Error(name); };
  page.on('pageerror', e => errors.push(e.message));
  await page.addInitScript(id => {
    sessionStorage.clear(); sessionStorage.setItem('netcap.selected.v1', id);
    window.skillStreams = [];
    window.EventSource = class {
      constructor() { this.listeners = {}; window.skillStreams.push(this); setTimeout(() => this.onopen?.(), 0); }
      addEventListener(name, fn) { this.listeners[name] = fn; }
      close() { this.closed = true; }
    };
  }, fixture.sessions[0]);
  let phase = 'updated-library', pending;
  await page.route('**/*', async route => {
    const r = route.request(), path = r.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    requests.push({ path, method: r.method(), body: r.postData() });
    if (path.endsWith('/skills/discover')) {
      if (phase === 'delayed') { pending = route; return; }
      if (phase === 'error') return route.fulfill({ status: 409, json: { error: '查询期间会话绑定或输入归属已变化，请重新查询 Skill 来源' } });
      return route.fulfill({ json: fixture.inventories[phase] });
    }
    if (fixture.responses[path]) return route.fulfill({ json: fixture.responses[path] });
    if (assets[path]) return route.fulfill({ path: assets[path], contentType: path.endsWith('.js') ? 'application/javascript' : path.endsWith('.css') ? 'text/css' : 'text/html' });
    return route.fulfill({ status: 404, json: { error: 'unmapped fixture route' } });
  });
  await page.setViewportSize({ width: 1440, height: 1100 });
  await page.goto('http://127.0.0.1:9467/');
  const open = async selector => {
    const target = page.locator(selector);
    if (await target.getAttribute('open') === null) await target.locator(':scope > summary').click();
  };
  await open('.execution-snapshot'); await open('.skill-discovery:not(.skill-context):not(.hook-catalog)');
  const panel = page.locator('.skill-discovery:not(.skill-context):not(.hook-catalog)'), refresh = panel.getByRole('button', { name: '刷新 Skill 来源', exact: true });
  const saved = panel.getByRole('region', { name: '发现查询所依据的保存选择' });
  check('no_automatic_discovery', !requests.some(r => r.path.endsWith('/skills/discover')));
  await refresh.click(); await panel.getByRole('status').waitFor();
  check('saved_version_from_snapshot', (await saved.innerText()).includes('版本 v1') && !(await saved.innerText()).includes('版本 v2'));
  check('saved_disabled_separate_from_directory_enabled', (await saved.innerText()).includes('已保存禁用') && (await panel.innerText()).includes('目录配置启用'));
  check('saved_snapshot_id_visible', (await saved.innerText()).includes(fixture.inventories.initial.snapshot_id));
  check('ordinary_entry_registration_guidance', (await panel.innerText()).includes('普通目录入口') && (await panel.innerText()).includes('登记此准确路径'));
  check('discovery_not_loading_or_method_evidence', (await panel.innerText()).includes('尚未核对本会话是否加载或使用') && (await saved.innerText()).includes('当前是否生效请查看执行核对结果'));
  check('obsolete_selection_unavailable_claim_removed', !(await panel.innerText()).includes('尚未接通逐会话启停'));
  await panel.scrollIntoViewIfNeeded();
  await page.screenshot({ path: '.dev/w0207-source-controls/saved-1440.png', fullPage: true });

  phase = 'ambiguous'; await refresh.click(); await panel.getByText('来源有歧义', { exact: true }).first().waitFor();
  check('both_same_name_sources_ambiguous', await panel.getByText('来源有歧义', { exact: true }).count() === 2);
  check('ambiguity_keeps_saved_reference', (await saved.innerText()).includes('版本 v1'));
  phase = 'partial'; await refresh.click(); await panel.getByRole('status').filter({ hasText: '不完整' }).waitFor();
  check('incomplete_source_controls_unknown', await panel.getByText('控制范围待核对', { exact: true }).count() >= 2);
  check('scan_error_source_visible', (await panel.innerText()).includes('broken/SKILL.md'));
  phase = 'repaired'; await refresh.click(); await panel.getByRole('status').filter({ hasText: '扫描完成' }).waitFor();
  check('repaired_clears_unknown_and_errors', await panel.getByText('控制范围待核对', { exact: true }).count() === 0 && await panel.locator('.error').count() === 0);
  phase = 'missing'; await refresh.click(); await saved.getByText(/本次清单未返回此路径/).waitFor();
  check('missing_source_does_not_remove_saved_selection', (await saved.innerText()).includes('已保存禁用') && (await saved.innerText()).includes('版本 v1'));
  check('missing_source_does_not_claim_inheritance', (await saved.innerText()).includes('不代表已撤销或已恢复继承'));
  await panel.scrollIntoViewIfNeeded();
  await page.screenshot({ path: '.dev/w0207-source-controls/missing-1440.png', fullPage: true });
  phase = 'error'; await refresh.click(); await panel.getByRole('alert').waitFor();
  check('binding_change_clears_all_old_results', await panel.getByRole('status').count() === 0 && await saved.count() === 0 && await panel.locator('.snapshot-asset').count() === 0);
  phase = 'delayed'; await refresh.click(); await panel.getByRole('button', { name: '正在查询 Skill…' }).waitFor();
  check('pending_query_cannot_repeat', await panel.getByRole('button', { name: '正在查询 Skill…' }).isDisabled());
  await page.locator('.session-row').filter({ hasText: 'Inherited methods' }).click();
  await open('.execution-snapshot'); await open('.skill-discovery:not(.skill-context):not(.hook-catalog)');
  if (!pending) throw new Error('missing delayed request');
  await pending.fulfill({ json: fixture.inventories.initial });
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  check('late_saved_selection_not_shown_in_other_session', await saved.count() === 0 && await panel.getByRole('status').count() === 0);
  await page.locator('.session-row').filter({ hasText: 'Selected method' }).click();
  await open('.execution-snapshot'); await open('.skill-discovery:not(.skill-context):not(.hook-catalog)');
  phase = 'missing'; await refresh.click(); await panel.getByRole('status').waitFor();
  await page.evaluate(() => window.skillStreams.filter(s => !s.closed).forEach(s => s.onerror?.()));
  await open('.skill-discovery:not(.skill-context):not(.hook-catalog)');
  check('disconnect_clears_saved_selection', await saved.count() === 0 && await panel.getByRole('status').count() === 0);
  check('disconnected_discovery_disabled', await refresh.isDisabled());
  await page.evaluate(() => window.skillStreams.filter(s => !s.closed).forEach(s => s.onopen?.()));
  await page.getByText('控制台已连接', { exact: true }).waitFor();
  await open('.skill-discovery:not(.skill-context):not(.hook-catalog)'); await refresh.waitFor({ state: 'visible' });
  check('reconnection_does_not_restore_stale_result', await saved.count() === 0);
  await refresh.click(); await panel.getByRole('status').waitFor();
  await page.setViewportSize({ width: 390, height: 844 });
  if (!await refresh.isVisible()) await page.getByRole('button', { name: '详情', exact: true }).click();
  await panel.scrollIntoViewIfNeeded();
  await page.screenshot({ path: '.dev/w0207-source-controls/missing-390.png', fullPage: true });
  check('mobile_no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
  check('discovery_has_no_mutation_requests', requests.filter(r => r.method === 'POST').every(r => fixture.sessions.some(id => r.path === `/api/sessions/${id}/skills/discover`) && r.body === '{}'));
  check('no_browser_errors', errors.length === 0);
  return { checks, errors, requests, scope: 'built UI with native Handler exports; HTTP transport/timing controlled, no live listener' };
}
