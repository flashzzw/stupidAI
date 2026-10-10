// Real Application responses; HTTP/SSE, legacy/history and lifecycle transitions are replayed.
async page => {
  const fixture = __FIXTURE__, assets = __ASSETS__, checks = {}, requests = [], errors = [];
  const sid = fixture.session_id, detailPath = '/api/sessions/' + sid;
  const clone = value => JSON.parse(JSON.stringify(value));
  const original = clone(fixture.responses[detailPath].session.execution_snapshot);
  let phase = 'drifted', fail = false, mismatch = false, delayed, release;
  const check = (name, ok) => { checks[name] = !!ok; if (!ok) throw Error(name); };
  page.on('pageerror', e => errors.push(e.message));
  await page.addInitScript(id => {
    sessionStorage.setItem('netcap.selected.v1', id);
    window.EventSource = class {
      constructor() { this.listeners = {}; window.settingsEvents = this; setTimeout(() => this.onopen?.(), 0); }
      addEventListener(name, fn) { this.listeners[name] = fn; }
      close() {}
    };
  }, sid);
  await page.route('**/*', async route => {
    const path = route.request().url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    requests.push({path, method:route.request().method()});
    if (path === detailPath + '/execution') {
      const value = clone(fixture.variants[phase]);
      value.snapshot.id = mismatch ? 'incorrect-snapshot' : fixture.responses[detailPath].session.execution_snapshot.id;
      if (delayed) await new Promise(resolve => { release = resolve; });
      if (fail) return route.fulfill({status:409,json:{error:'fixture source read failed'}});
      return route.fulfill({json:value});
    }
    if (fixture.responses[path]) return route.fulfill({json:fixture.responses[path]});
    if (assets[path]) return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'unmapped fixture'});
  });
  await page.setViewportSize({width:1440,height:1100});
  await page.goto('http://127.0.0.1:9455/');
  const panel = page.locator('.execution-snapshot'), local = panel.locator('.local-settings-sources').first();
  const sourceCheck = panel.locator('.execution-source-check');
  await panel.locator(':scope > summary').click();
  await local.locator('summary').click();
  const text = await local.innerText(), sources = original.claude_local_settings.sources;
  check('original_source_paths', sources.every(s => text.includes(s.path)));
  check('original_hash', sources.filter(s => s.kind === 'file').every(s => text.includes(s.sha256)));
  check('absent_paths', await local.getByText('记录时不存在',{exact:true}).count() === sources.filter(s => s.kind === 'absent').length);
  check('original_permissions', text.includes('记录的文件权限：600'));
  check('coverage_limits', text.includes('不代表原生实际加载') && text.includes('也不冻结原生文件'));
  check('read_is_manual', requests.every(r => r.path !== detailPath + '/execution'));
  const read = async () => { await sourceCheck.getByRole('button').click(); await sourceCheck.getByRole('status').waitFor(); };
  await read();
  check('native_drift_visible', (await sourceCheck.innerText()).includes('来源或绑定已变化'));
  check('drift_path_visible', (await sourceCheck.innerText()).includes(sources.find(s=>s.kind==='file').path));
  check('drift_preserves_original_hash', (await local.innerText()).includes(sources.find(s=>s.kind==='file').sha256));
  await local.screenshot({path:'.dev/w0208-settings-version/local-1440.png'});
  await page.setViewportSize({width:390,height:844});
  await page.getByRole('button',{name:'详情',exact:true}).click();
  await sourceCheck.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow', await page.evaluate(()=>document.documentElement.scrollWidth <= innerWidth+1));
  await sourceCheck.screenshot({path:'.dev/w0208-settings-version/drift-390.png'});
  await page.setViewportSize({width:1440,height:1100});
  phase = 'restored'; await read();
  check('restored_read_visible', (await sourceCheck.innerText()).includes('已记录的来源与绑定未发现变化。'));
  fail = true; await sourceCheck.getByRole('button').click(); await sourceCheck.getByRole('alert').waitFor();
  check('failed_read_clears_success', await sourceCheck.getByRole('status').count() === 0);
  check('failed_read_error', (await sourceCheck.innerText()).includes('fixture source read failed'));
  fail = false; mismatch = true; await sourceCheck.getByRole('button').click();
  await sourceCheck.getByText('执行版本已变化，请刷新后重新核对来源。',{exact:true}).waitFor();
  check('mismatched_snapshot_rejected', await sourceCheck.getByRole('status').count() === 0);
  mismatch = false; await read();
  const update = async (snapshot, history) => {
    for (const p of ['/api/workspace','/api/snapshot',detailPath]) {
      const response = fixture.responses[p]; response.sequence += 100;
      const session = response.session ?? response.sessions.find(s=>s.id===sid);
      if (session.execution_snapshot) session.execution_snapshot = clone(snapshot);
      session.execution_history = history;
    }
    await page.evaluate(()=>window.settingsEvents.listeners.change());
    await panel.getByText(snapshot.id,{exact:true}).waitFor();
  };
  const legacy = clone(original); delete legacy.claude_local_settings; legacy.id += '-legacy';
  await update(legacy, []);
  check('new_snapshot_clears_result', await sourceCheck.getByRole('status').count() === 0);
  check('legacy_unrecorded', (await local.innerText()).includes('此快照未记录项目和本地设置版本'));
  const next = {...original,id:original.id+'-next',revision:2};
  await update(next, [original, legacy]);
  const history = panel.getByRole('region',{name:'历史执行快照'});
  for (const row of await history.locator(':scope > details').all()) {
    await row.locator(':scope > summary').click();
    await row.locator('.local-settings-sources > summary').click();
  }
  check('historical_version_preserved', (await history.innerText()).includes(sources.find(s=>s.kind==='file').sha256));
  check('historical_legacy_unrecorded', (await history.innerText()).includes('此快照未记录项目和本地设置版本'));
  await read();
  delayed = true;
  await sourceCheck.getByRole('button').click();
  await sourceCheck.getByRole('button',{name:'正在核对…'}).waitFor();
  check('pending_read_clears_old_result', await sourceCheck.getByRole('status').count() === 0);
  for (let i=0; !release && i<50; i++) await page.waitForTimeout(20);
  check('request_delayed', !!release);
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));
  await page.waitForFunction(()=>document.querySelector('.execution-source-check button')?.disabled && document.querySelector('.execution-source-check button')?.textContent === '核对当前来源');
  check('disconnect_disables_read', await sourceCheck.getByRole('button').isDisabled());
  release(); delayed = false; await page.waitForTimeout(100);
  check('late_reply_ignored_after_disconnect', await sourceCheck.getByRole('status').count() === 0);
  await page.evaluate(()=>window.dispatchEvent(new Event('online')));
  await page.waitForFunction(()=>!document.querySelector('.execution-source-check button')?.disabled);
  check('reconnect_has_no_old_result', await sourceCheck.getByRole('status').count() === 0);
  await read();
  check('fresh_read_after_reconnect', await sourceCheck.getByRole('status').count() === 1);
  check('no_execution_commands', requests.every(r=>r.path!=='/api/commands'));
  check('source_reads_use_get', requests.filter(r=>r.path.endsWith('/execution')).every(r=>r.method==='GET'));
  check('no_browser_errors', errors.length === 0);
  return {checks,requests,errors,scope:'real Application source reads; controlled HTTP/SSE and legacy/history/lifecycle display; no native settings adoption'};
}
