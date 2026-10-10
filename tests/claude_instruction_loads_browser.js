// Replay production Application/Handler frames from the isolated native test.
// Legacy, missing, failure, truncation and unknown metadata are display fixtures.
async page => {
  page = await page.context().newPage();
  const fixture = __FIXTURE__, assets = __ASSETS__, output = __OUTPUT__;
  const checks = {}, requests = [], errors = [], unmapped = [];
  const sid = fixture.sessions.a, other = fixture.sessions.b;
  const copy = value => JSON.parse(JSON.stringify(value));
  const check = (name, value) => {checks[name] = Boolean(value); if (!value) throw Error(name);};
  const detailPath = id => '/api/sessions/' + id;
  const history = (frame, id = sid) => fixture.frames[frame][detailPath(id)].session.view.instruction_loads;
  let frame = 'initial', mode = 'native';
  page.setDefaultTimeout(8000);
  page.on('pageerror', error => errors.push(error.message));
  await page.addInitScript(id => {
    if (location.origin !== 'http://127.0.0.1:9468') return;
    sessionStorage.clear();
    sessionStorage.setItem('netcap.selected.v1', id);
    window.EventSource = class {
      constructor() {setTimeout(() => this.onopen?.(), 0);}
      addEventListener() {}
      close() {}
    };
  }, sid);
  const contract = session => {
    if (session.id !== sid || mode === 'native') return;
    const h = session.view?.instruction_loads;
    if (!h) return;
    if (mode === 'legacy') delete session.view.instruction_loads;
    if (mode === 'empty') h.items = [];
    if (mode === 'failed') h.recording_failed = true;
    if (mode === 'unavailable-failure') {h.available = false; h.recording_failed = true; h.items = [];}
    if (mode === 'truncated') {
      h.truncated = true;
      h.items = Array.from({length: 128}, (_, i) => ({...copy(h.items[0]), id: i.toString(16).padStart(8, '0') + '-1111-4111-8111-111111111111', path: '/contract/truncated/' + i + '/CLAUDE.md'}));
    }
    if (mode === 'unknown') h.items = [{...copy(h.items[0]), path: '/contract/unknown/CLAUDE.md', source: 'FutureSource', reason: 'future_load', globs: ['feature/**'], native_cwd: '/contract/changed-cwd'}];
    if (mode === 'subagent') {
      const load = {...copy(fixture.nested), agent_id: 'contract-child', agent_type: 'reviewer'};
      delete load.operation; delete load.execution_snapshot_id; delete load.input_correlation;
      h.items = [load];
    }
  };
  await page.route('**/*', async route => {
    const request = route.request(), path = request.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    requests.push({path, method: request.method()});
    if (fixture.frames[frame][path]) {
      const value = copy(fixture.frames[frame][path]);
      if (value.session) contract(value.session);
      if (value.sessions) value.sessions.forEach(contract);
      return route.fulfill({json: value});
    }
    if (assets[path]) return route.fulfill({path: assets[path], contentType: path.endsWith('.js') ? 'application/javascript' : path.endsWith('.css') ? 'text/css' : path.endsWith('.svg') ? 'image/svg+xml' : 'text/html'});
    if (path.startsWith('/favicon.')) return route.fulfill({status: 204});
    unmapped.push(path);
    return route.fulfill({status: 404, body: 'Unmapped instruction loads acceptance request'});
  });
  const panel = page.locator('.instruction-loads');
  const rows = panel.locator(':scope > .snapshot-asset');
  const openPanel = async () => {
    const details = page.locator('.execution-snapshot');
    await details.waitFor({state: 'attached'});
    if (!await details.isVisible()) await page.getByRole('button', {name: '详情', exact: true}).click();
    if (await details.getAttribute('open') === null) await details.locator(':scope > summary').click();
    if (await panel.getAttribute('open') === null) await panel.locator(':scope > summary').click();
  };
  const load = async (nextFrame, nextMode = 'native') => {
    frame = nextFrame; mode = nextMode;
    await page.goto('http://127.0.0.1:9468/');
    await openPanel();
  };
  const countLabel = async label => rows.locator(':scope > p').filter({hasText: label}).count();
  const screenshot = async (label, target = panel) => {
    await target.evaluate(element => {
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
    await load('initial');
    const startup = await panel.innerText();
    check('seven_native_startup_sources_visible', history('initial').items.length === 7 && history('initial').items.every(item => startup.includes(item.path)));
    check('all_startup_records_belong_to_current_query', await countLabel('当前查询') === 7);
    check('native_source_labels_visible', ['来源：用户', '来源：项目（含上层目录）', '来源：项目本地', '来源：管理员受管'].every(label => startup.includes(label)));
    check('native_import_parent_visible', history('initial').items.some(item => item.parent_path && startup.includes('导入文件：' + item.parent_path)));
    check('startup_without_prompt_does_not_invent_input', startup.includes('本通知没有原生提示 ID，不指定输入操作。'));
    check('loaded_version_and_permission_limits_visible', startup.includes('不证明内容版本') && startup.includes('不授予执行权限'));
    check('instruction_body_not_rendered', !startup.includes('PRIVATE_INSTRUCTION'));
    await noOverflow('desktop_startup');
    await screenshot('startup-1440');

    await load('nested');
    const nested = rows.filter({has: page.getByText(fixture.nested.path, {exact: true})});
    const nestedText = await nested.innerText();
    check('read_triggered_nested_source_visible', nestedText.includes('访问子目录 · 当前查询') && nestedText.includes('触发文件：' + fixture.nested.trigger_path));
    check('nested_original_input_visible', nestedText.includes('关联原操作：' + fixture.nested.operation));
    check('nested_original_snapshot_visible', nestedText.includes('原操作的快照：' + fixture.original_snapshot));
    await nested.locator('details > summary').click();
    check('independent_prompt_and_input_ids_preserved', fixture.nested.prompt_id !== fixture.nested.operation && (await nested.innerText()).includes('原生提示 ID：' + fixture.nested.prompt_id));
    check('native_tool_link_basis_visible', (await nested.innerText()).includes('通过同一查询的原生工具事件核对输入 UUID'));
    await noOverflow('desktop_nested');
    await screenshot('nested-1440', nested);
    await page.setViewportSize({width: 390, height: 844});
    await openPanel();
    await noOverflow('mobile_nested');
    await screenshot('nested-390', nested);

    await load('offline');
    check('workbench_restart_preserves_all_eight_records', await rows.count() === 8);
    check('offline_records_are_saved_not_current', await countLabel('已保存记录') === 8 && await countLabel('当前查询') === 0);
    check('offline_original_input_is_preserved', (await panel.innerText()).includes(fixture.nested.operation));
    await screenshot('offline-390');

    await load('restored');
    check('original_id_resume_keeps_fifteen_notifications', await rows.count() === 15);
    check('resume_separates_current_and_prior_queries', await countLabel('当前查询') === 7 && await countLabel('先前查询') === 8);
    const prior = rows.filter({has: page.getByText(fixture.nested.path, {exact: true})});
    check('restored_nested_notice_keeps_original_operation_and_snapshot', (await prior.innerText()).includes(fixture.nested.operation) && (await prior.innerText()).includes(fixture.original_snapshot));
    await screenshot('restored-390', prior);

    await page.setViewportSize({width: 1440, height: 1100});
    await load('nested');
    await page.locator('.session-row').filter({hasText: 'Instruction loads B'}).click();
    await openPanel();
    check('session_switch_shows_its_own_seven_records', await rows.count() === 7 && !(await panel.innerText()).includes(fixture.nested.path));
    await rows.first().locator('details > summary').click();
    check('second_session_query_identity_visible', (await rows.first().innerText()).includes(history('nested', other).current_query_id));
    await page.evaluate(() => window.dispatchEvent(new Event('offline')));
    await panel.getByText('当前原生连接尚未核对；保留已保存通知，不把历史记录当作当前加载。', {exact: true}).waitFor({state: 'attached'});
    await openPanel();
    check('disconnect_preserves_records_without_current_claim', await rows.count() === 7 && await countLabel('已保存记录') === 7 && await countLabel('当前查询') === 0);

    for (const value of ['legacy', 'empty', 'failed', 'unavailable-failure', 'truncated', 'unknown', 'subagent']) {
      await load('nested', value);
      const text = await panel.innerText();
      if (value === 'legacy') check('legacy_bridge_does_not_claim_empty_coverage', await rows.count() === 0 && text.includes('尚无可核对的加载通知记录；这不表示没有加载指令。'));
      if (value === 'empty') check('empty_observation_does_not_claim_no_instructions', await rows.count() === 0 && text.includes('空记录不能证明未加载指令。'));
      if (value === 'failed' || value === 'unavailable-failure') check(value + '_shows_recording_gap', text.includes('部分加载通知未能保存或核对'));
      if (value === 'truncated') check('truncation_is_visible_without_claiming_deleted_journal', await rows.count() === 128 && text.includes('更早的日记记录仍保留'));
      if (value === 'unknown') {
        check('unknown_native_source_and_reason_preserved', text.includes('FutureSource') && text.includes('future_load'));
        check('native_globs_and_changed_cwd_visible', text.includes('feature/**') && text.includes('通知时的工作目录：/contract/changed-cwd'));
      }
      if (value === 'subagent') check('subagent_prompt_does_not_acquire_main_operation', text.includes('原生子代理：contract-child · reviewer') && text.includes('原生提示尚未关联工作台输入操作。') && !text.includes('关联原操作：'));
    }
    check('no_execution_commands_or_new_source_requests', requests.every(request => request.path !== '/api/commands' && !request.path.endsWith('/skills/catalog')));
    check('all_http_requests_mapped', unmapped.length === 0);
    check('no_browser_errors', errors.length === 0);
    return {passed: true, checks, requests, errors, scope: 'native Application/Handler frames for startup, Read, restart and original-ID resume; explicit synthetic boundary displays'};
  } catch (error) {
    await page.screenshot({path: output + '/failure.png'});
    return {passed: false, checks, requests, errors, unmapped, error: String(error)};
  } finally {
    await page.close();
  }
}
