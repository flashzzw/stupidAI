// Native Application responses; HTTP/SSE and UI lifecycle changes are replayed.
async page => {
  const fixture = __FIXTURE__, assets = __ASSETS__, checks = {}, requests = [], errors = [];
  const sid = fixture.sessions.a;
  let phase = 'native', fail = false;
  const check = (name, ok) => { checks[name] = !!ok; if (!ok) throw Error(name); };
  page.on('pageerror', e => errors.push(e.message));
  await page.addInitScript(id => {
    sessionStorage.setItem('netcap.selected.v1', id);
    window.EventSource = class {
      constructor() { this.listeners = {}; window.catalogEvents = this; setTimeout(() => this.onopen?.(), 0); }
      addEventListener(name, fn) { this.listeners[name] = fn; }
      close() {}
    };
  }, sid);
  await page.route('**/*', async route => {
    const req = route.request(), path = req.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    requests.push({path, method:req.method(), body:req.postData() ? JSON.parse(req.postData()) : null});
    if (path.endsWith('/plugins/installed')) {
      if (fail) return route.fulfill({status:409,json:{error:'读取期间会话绑定或输入归属已变化，请重新读取插件登记'}});
      const value = JSON.parse(JSON.stringify(fixture.variants[phase] ?? fixture.variants.native));
      if (phase === 'errors') { value.complete = false; value.entries = []; value.errors = [{path:value.registry_path,detail:'登记元数据 JSON 无效，未返回正文或解析输入'}]; }
      if (phase === 'remote') { value.complete = false; value.entries[0] = {...value.entries[0],market_kind:'github',market_path:undefined,market_install_path:undefined,declared_path:undefined,resolved_path:undefined,manifest_version:undefined,status:'unsupported',detail:'此市场类型尚无本地声明路径核对；不会下载、安装或推断实际加载位置。'}; }
      return route.fulfill({json:value});
    }
    if (fixture.responses[path]) return route.fulfill({json:fixture.responses[path]});
    if (assets[path]) return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:404,body:'unmapped fixture'});
  });
  await page.setViewportSize({width:1440,height:1100});
  await page.goto('http://127.0.0.1:9455/');
  await page.locator('.execution-snapshot>summary').click();
  const panel = page.locator('details').filter({has:page.locator('summary').getByText('Claude 已安装插件登记',{exact:true})}).last();
  await panel.locator('summary').click();
  const read = async () => { await panel.getByRole('button',{name:'读取安装登记',exact:true}).click(); await panel.getByRole('status').waitFor(); };
  check('manual_read_only', requests.every(r=>!r.path.endsWith('/plugins/installed')));
  await read();
  const entry = fixture.variants.native.entries[0];
  check('full_source_key_and_scope', (await panel.innerText()).includes(entry.source_key+' · 用户登记'));
  check('registry_path', (await panel.innerText()).includes(fixture.variants.native.registry_path));
  check('cache_path', (await panel.innerText()).includes('登记缓存：'+entry.cache_path));
  check('market_declaration_path', (await panel.innerText()).includes('市场声明的插件目录：'+entry.declared_path));
  check('versions_separately_labeled', (await panel.innerText()).includes('登记版本：1.0.0') && (await panel.innerText()).includes('当前文件声明版本：1.0.0'));
  check('load_and_permission_limits', (await panel.innerText()).includes('不代表此会话从这里加载') && (await panel.innerText()).includes('也不授权启停'));
  await panel.scrollIntoViewIfNeeded();
  await panel.screenshot({path:'.dev/w0208-installed-catalog/native-1440.png'});
  phase = 'drift'; await read();
  check('drift_keeps_both_versions', (await panel.innerText()).includes('登记版本：1.0.0') && (await panel.innerText()).includes('当前文件声明版本：2.0.0'));
  check('drift_is_incomplete', (await panel.innerText()).includes('登记采样不完整') && (await panel.innerText()).includes('登记与文件版本不同'));
  await page.setViewportSize({width:390,height:844});
  await page.getByRole('button',{name:'详情',exact:true}).click();
  await panel.scrollIntoViewIfNeeded();
  check('mobile_no_horizontal_overflow', await page.evaluate(()=>document.documentElement.scrollWidth <= innerWidth+1));
  await panel.screenshot({path:'.dev/w0208-installed-catalog/drift-390.png'});
  await page.setViewportSize({width:1440,height:1100});
  phase = 'unknown'; await read();
  check('unknown_has_no_invented_path', !(await panel.innerText()).includes('市场声明的插件目录：') && (await panel.innerText()).includes('没有可读取的市场登记'));
  phase = 'remote'; await read();
  check('remote_type_limit', (await panel.innerText()).includes('市场登记类型：github') && (await panel.innerText()).includes('来源尚未展开'));
  phase = 'errors'; await read();
  check('metadata_error_visible', (await panel.innerText()).includes('登记元数据 JSON 无效') && (await panel.innerText()).includes('登记采样不完整'));
  check('empty_not_claiming_no_plugins', (await panel.innerText()).includes('不代表没有其他原生、受管或动态插件来源'));
  phase = 'native'; await read(); fail = true;
  await panel.getByRole('button',{name:'读取安装登记',exact:true}).click();
  await panel.getByRole('alert').waitFor();
  check('failed_read_clears_old_entries', await panel.locator('.snapshot-asset').count()===0);
  check('binding_failure_visible', (await panel.innerText()).includes('会话绑定或输入归属已变化'));
  fail = false; await read();
  // Controlled newer snapshot tests remounting; no native transaction is implied.
  const changedID = fixture.responses['/api/sessions/'+sid].session.execution_snapshot.id+'-ui-next';
  for (const path of ['/api/workspace','/api/snapshot']) {
    fixture.responses[path].sequence += 100;
    if (fixture.responses[path].sessions[0].execution_snapshot) fixture.responses[path].sessions[0].execution_snapshot.id = changedID;
  }
  fixture.responses['/api/sessions/'+sid].sequence += 100;
  fixture.responses['/api/sessions/'+sid].session.execution_snapshot.id = changedID;
  await page.evaluate(()=>window.catalogEvents.listeners.change());
  await page.getByText(changedID,{exact:true}).waitFor();
  await panel.locator('summary').click();
  check('new_snapshot_clears_old_entries', await panel.locator('.snapshot-asset').count()===0);
  await read();
  await page.evaluate(()=>window.dispatchEvent(new Event('offline')));
  await panel.getByText('工作台已断开，连接后可重新读取。',{exact:true}).waitFor({state:'attached'});
  await panel.locator('summary').click();
  check('disconnect_clears_old_entries', await panel.locator('.snapshot-asset').count()===0);
  check('disconnect_disables_read', await panel.getByRole('button',{name:'读取安装登记',exact:true}).isDisabled());
  check('empty_post_contract', requests.filter(r=>r.path.endsWith('/plugins/installed')).every(r=>r.method==='POST' && JSON.stringify(r.body)==='{}'));
  check('no_execution_commands', requests.every(r=>r.path!=='/api/commands'));
  check('no_browser_errors', errors.length===0);
  return {checks,requests,errors,scope:'real native installation Application responses; controlled HTTP/SSE, remote/error and snapshot-change display cases'};
}
