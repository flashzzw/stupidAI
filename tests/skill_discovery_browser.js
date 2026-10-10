// Replay production Handler exports from TestCodexNativeSkillDiscovery.
// Native/SQLite reads already ran in the Go test; browser HTTP delivery and
// connection/failure timing are controlled here, without a listening socket.
async page => {
  await page.unrouteAll({ behavior: 'ignoreErrors' });
  const fixture = __FIXTURE__, assets = __ASSETS__;
  const checks = {}, requests = [], errors = [];
  page.__skills = { checks, requests, errors };
  const check = (name, ok) => { checks[name] = !!ok; if (!ok) throw new Error(name); };
  page.on('pageerror', e => errors.push(e.message));
  await page.addInitScript(id => {
    sessionStorage.setItem('netcap.selected.v1', id);
    window.__skillStreams = [];
    window.EventSource = class {
      constructor() { window.__skillStreams.push(this); setTimeout(() => this.onopen?.(), 0); }
      addEventListener() {}
      close() { this.closed = true; }
    };
  }, fixture.sessions[0]);
  let mode = 'partial', pending;
  await page.route('**/*', async route => {
    const request = route.request(), path = request.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    requests.push({ path, method: request.method(), body: request.postData() });
    if (path.endsWith('/skills/discover')) {
      if (mode === 'delay') { pending = route; return; }
      if (mode === 'error') return route.fulfill({ status: 503, json: { error: '受控传输故障，未取得本次发现结果' } });
      return route.fulfill({ json: fixture.inventories[mode] });
    }
    if (fixture.responses[path]) return route.fulfill({ json: fixture.responses[path] });
    if (assets[path]) return route.fulfill({ path: assets[path], contentType: path.endsWith('.js') ? 'application/javascript' : path.endsWith('.css') ? 'text/css' : 'text/html' });
    return route.fulfill({ status: 404, body: 'unmapped fixture route' });
  });
  await page.setViewportSize({ width: 1440, height: 1100 });
  await page.goto('http://127.0.0.1:9454/');
  await page.locator('.execution-snapshot > summary').click();
  await page.locator('.skill-discovery:not(.skill-context):not(.hook-catalog) > summary').click();
  const panel = page.locator('.skill-discovery:not(.skill-context):not(.hook-catalog)');
  const refresh = page.getByRole('button', { name: '刷新 Skill 来源', exact: true });
  check('no_automatic_scan', !requests.some(r => r.path.endsWith('/skills/discover')));
  await refresh.click();
  await panel.getByRole('status').filter({ hasText: '不完整' }).waitFor();
  check('partial_error_source_visible', (await panel.innerText()).includes('broken/SKILL.md'));
  check('user_and_project_sources_visible', (await panel.innerText()).includes('来源：用户') && (await panel.innerText()).includes('来源：项目'));
  check('directory_disabled_distinct', (await panel.innerText()).includes('目录配置禁用'));
  check('does_not_claim_loaded_or_used', (await panel.innerText()).includes('尚未核对本会话是否加载或使用'));
  check('exclusion_not_claimed', (await panel.innerText()).includes('完整资源与实际选择须在登记、改配和执行时另行核对'));
  mode = 'repaired';
  await refresh.click();
  await panel.getByRole('status').filter({ hasText: '扫描完成' }).waitFor();
  check('refresh_replaces_errors', await panel.locator('.error').count() === 0);
  check('newly_discovered_skill_visible', (await panel.innerText()).includes('repaired-method'));
  await page.screenshot({ path: '.dev/w0207/skill-discovery-1440.png', fullPage: true });
  mode = 'error';
  await refresh.click();
  await panel.getByRole('alert').waitFor();
  check('failed_refresh_clears_previous_result', await panel.getByRole('status').count() === 0 && await panel.locator('.snapshot-asset').count() === 0);
  mode = 'delay';
  await refresh.click();
  await page.getByRole('button', { name: '正在查询 Skill…' }).waitFor();
  check('pending_refresh_disabled', await page.getByRole('button', { name: '正在查询 Skill…' }).isDisabled());
  await page.locator('.session-row').filter({ hasText: 'Second discovery session' }).click();
  await page.locator('.execution-snapshot > summary').click();
  await page.locator('.skill-discovery:not(.skill-context):not(.hook-catalog) > summary').click();
  if (!pending) throw new Error('delayed request not captured');
  await pending.fulfill({ json: fixture.inventories.partial });
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  check('late_reply_not_shown_in_other_session', await panel.getByRole('status').count() === 0);
  mode = 'removed';
  await refresh.click();
  await panel.getByRole('status').waitFor();
  check('deleted_skill_absent', !(await panel.innerText()).includes('project-method'));
  await page.evaluate(() => window.__skillStreams.filter(s => !s.closed).forEach(s => s.onerror?.()));
  await page.locator('.skill-discovery:not(.skill-context):not(.hook-catalog) > summary').click();
  check('disconnect_clears_result', await panel.getByRole('status').count() === 0);
  check('disconnected_refresh_disabled', await refresh.isDisabled());
  await page.evaluate(() => window.__skillStreams.filter(s => !s.closed).forEach(s => s.onopen?.()));
  await page.getByText('控制台已连接', { exact: true }).waitFor();
  await page.locator('.skill-discovery:not(.skill-context):not(.hook-catalog) > summary').click();
  await refresh.waitFor({ state: 'visible' });
  check('reconnect_keeps_result_unqueried', await panel.getByRole('status').count() === 0);
  await refresh.click();
  await panel.getByRole('status').waitFor();
  await page.setViewportSize({ width: 390, height: 844 });
  if (!(await refresh.isVisible())) await page.getByRole('button', { name: '详情', exact: true }).click();
  await page.screenshot({ path: '.dev/w0207/skill-discovery-390.png', fullPage: true });
  check('mobile_no_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
  check('only_bound_empty_discovery_requests', requests.filter(r => r.method === 'POST').every(r => fixture.sessions.some(id => r.path === `/api/sessions/${id}/skills/discover`) && r.body === '{}'));
  check('no_browser_errors', errors.length === 0);
  return { checks, requests, errors, scope: 'built UI, recorded native Handler responses; controlled transport failures and connection, no live HTTP or Unix proxy' };
}
