// Browser interactions replay actual native Application/Handler snapshots.
// Transport errors and unknown versions are display-only fixtures.
async page => {
  const fixtures = __FIXTURES__, assets = __ASSETS__, output = __OUTPUT__;
  const checks = {}, commands = [], errors = [], unmapped = [];
  const check = (name, ok) => { checks[name] = !!ok; if (!ok) throw Error(name); };
  const clone = value => JSON.parse(JSON.stringify(value));
  const original = page;
  const pages = [];
  async function open(name, stage, variant = '') {
    const fixture = fixtures[name], sid = fixture.sessions.A, base = '/api/sessions/' + sid;
    const tab = await original.context().newPage(); pages.push(tab); tab.setDefaultTimeout(8000);
    let frame = clone(fixture.frames[stage]);
    const getSession = value => value.session ?? value;
    const session = getSession(frame[base]);
    const pending = session.view.pending.find(p => p.status === 'pending');
    if (variant === 'unknown') { pending.permission_guard.version = 999; }
    if (variant === 'submitted') pending.status = 'submitted';
    // Both list and detail endpoints expose the same controlled display state.
    if (variant) {
      const workspace = frame['/api/workspace'];
      const listed = workspace.sessions?.find(s => s.id === sid);
      if (listed?.view) listed.view.pending = clone(session.view.pending);
    }
    tab.on('pageerror', error => errors.push(error.message));
    await tab.addInitScript(id => {
      if (location.origin !== 'http://127.0.0.1:9468') return;
      sessionStorage.clear(); sessionStorage.setItem('netcap.selected.v1', id);
      window.EventSource = class { constructor() { setTimeout(() => this.onopen?.(), 0); } addEventListener() {} close() {} };
    }, sid);
    await tab.route('**/*', async route => {
      const request = route.request(), path = request.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
      if (path === '/api/commands') {
        const body = request.postDataJSON(); commands.push({name, variant, body});
        const expected = fixture.http.find(row => row.path === path && row.request.kind === body.kind && row.request.option_id === body.option_id);
        if (!expected) { unmapped.push(name+':'+JSON.stringify(body)); return route.fulfill({status:409,json:{error:'Unexpected fixture command'}}); }
        frame = clone(fixture.frames.finished);
        return route.fulfill({status:expected.status,json:expected.response});
      }
      if (frame[path]) return route.fulfill({json:frame[path]});
      if (assets[path]) return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':path.endsWith('.svg')?'image/svg+xml':'text/html'});
      if (path.startsWith('/favicon.')) return route.fulfill({status:204});
      unmapped.push(name+':'+path); return route.fulfill({status:404,body:'Unmapped pending permission fixture'});
    });
    await tab.setViewportSize({width:1440,height:1000}); await tab.goto('http://127.0.0.1:9468/');
    const panel = tab.locator('.pending-panel').filter({hasText:pending.title}).first();
    await panel.waitFor();
    return {tab, panel, pending, sid};
  }
  async function capture(tab, panel, name) {
    await panel.scrollIntoViewIfNeeded();
    await tab.screenshot({path:output+'/'+name+'.png'});
    check(name+'_no_horizontal_overflow', await tab.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  }
  async function respond(view, label) {
    await Promise.all([view.tab.waitForResponse(response=>response.url().endsWith('/api/commands')),
      view.panel.getByRole('button',{name:label,exact:true}).click()]);
  }
  try {
    const normal = await open('unchanged','pending');
    check('unchanged_observation_and_limitation_visible', (await normal.panel.innerText()).includes('尚未观察到权限规则变化') && (await normal.panel.innerText()).includes('不保证'));
    check('unchanged_allow_and_deny_available', await normal.panel.getByRole('button',{name:'允许本次',exact:true}).isEnabled() && await normal.panel.getByRole('button',{name:'拒绝本次',exact:true}).isEnabled());
    await capture(normal.tab,normal.panel,'unchanged-1440');
    await respond(normal,'允许本次');
    check('allow_uses_original_request', commands.at(-1)?.body.pending_id===normal.pending.id && commands.at(-1)?.body.session_id===normal.sid && commands.at(-1)?.body.option_id==='allow');
    const changed = await open('deny','after-allow');
    check('changed_reason_visible', (await changed.panel.innerText()).includes('权限规则变化') && (await changed.panel.innerText()).includes('不能批准'));
    check('changed_allow_disabled', await changed.panel.getByRole('button',{name:'允许本次',exact:true}).isDisabled());
    check('changed_deny_available', await changed.panel.getByRole('button',{name:'拒绝本次',exact:true}).isEnabled());
    await capture(changed.tab,changed.panel,'changed-1440');
    await changed.tab.setViewportSize({width:390,height:844}); await capture(changed.tab,changed.panel,'changed-390');
    await respond(changed,'拒绝本次');
    check('deny_uses_original_request', commands.at(-1)?.body.pending_id===changed.pending.id && commands.at(-1)?.body.session_id===changed.sid && commands.at(-1)?.body.option_id==='deny');
    const restored = await open('restore','after-allow');
    check('restored_rules_do_not_reenable_old_approval', await restored.panel.getByRole('button',{name:'允许本次',exact:true}).isDisabled());
    check('restored_rejection_stays_available', await restored.panel.getByRole('button',{name:'拒绝本次',exact:true}).isEnabled());
    const unavailable = await open('user-invalid','after-allow');
    check('missing_observation_reason_visible', (await unavailable.panel.innerText()).includes('观察不完整'));
    check('unavailable_allow_disabled', await unavailable.panel.getByRole('button',{name:'允许本次',exact:true}).isDisabled());
    check('unavailable_deny_available', await unavailable.panel.getByRole('button',{name:'拒绝本次',exact:true}).isEnabled());
    await capture(unavailable.tab,unavailable.panel,'unavailable-1440');
    const local = await open('local-drift','after-allow');
    check('local_source_drift_keeps_refusal_available', await local.panel.getByRole('button',{name:'拒绝本次',exact:true}).isEnabled());
    await respond(local,'拒绝本次');
    check('local_refusal_keeps_original_identity', commands.at(-1)?.body.pending_id===local.pending.id && commands.at(-1)?.body.session_id===local.sid);
    const unknown = await open('unchanged','pending','unknown');
    check('unknown_guard_version_cannot_approve', await unknown.panel.getByRole('button',{name:'允许本次',exact:true}).isDisabled());
    check('unknown_guard_version_can_refuse', await unknown.panel.getByRole('button',{name:'拒绝本次',exact:true}).isEnabled());
    check('unknown_guard_version_has_unavailable_reason', (await unknown.panel.innerText()).includes('观察不可用'));
    const submitted = await open('unchanged','pending','submitted');
    check('submitted_request_cannot_repeat', await submitted.panel.getByRole('button',{name:'允许本次',exact:true}).isDisabled() && await submitted.panel.getByRole('button',{name:'拒绝本次',exact:true}).isDisabled());
    await restored.tab.evaluate(()=>window.dispatchEvent(new Event('offline')));
    await restored.tab.waitForFunction(()=>[...document.querySelectorAll('.pending-panel button')].some(button=>button.textContent==='拒绝本次'&&button.disabled));
    check('offline_refusal_disabled', await restored.panel.getByRole('button',{name:'拒绝本次',exact:true}).isDisabled());
    check('only_three_explicit_original_responses',commands.length===3 && commands.every(c=>c.body.kind==='respond'));
    check('no_browser_errors',errors.length===0);check('no_unmapped_requests',unmapped.length===0);
    return {checks,commands,errors,unmapped,passed:Object.values(checks).every(Boolean)};
  } catch (error) {
    const last=pages.at(-1);
    if(last)await last.screenshot({path:output+'/failure.png'});
    return {checks,commands,errors,unmapped,passed:false,failure:String(error),last_page:last?await last.locator('body').innerText():''};
  } finally {for (const tab of pages) await tab.close();}
}
