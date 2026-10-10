// Tool-host driver for functions.exec. Before running, set authUISession to the
// live JSON-line runner handle and authUIRoot to its absolute owned root; select
// a fresh Playwright tab. All browser POSTs go to that live Handler unchanged.
(async () => {
 const root=load('authUIRoot'),session=load('authUISession');
 if(!root||!session)throw Error('live fixture root and handle required');
 const q=s=>"'"+s.replaceAll("'","'\\''")+"'";
 const checks={},events=[];
 const check=(n,v)=>{checks[n]=!!v;if(!v)throw Error(n)};
 const file=async name=>{
  const r=await tools.exec_command({cmd:'cat '+q(name),max_output_tokens:30000});
  if(r.exit_code!==0)throw Error(r.output);return r.output;
 };
 const browser=async code=>{
  const r=await tools.mcp__playwright__browser_run_code_unsafe({code});
  const t=r.content?.find(x=>x.type==='text')?.text??'';
  const match=t.match(/### Result\n([\s\S]*?)(?:\n###|$)/);
  if(!match)throw Error(t);return JSON.parse(match[1]);
 };
 const send=async request=>{
  const r=await tools.write_stdin({session_id:session,chars:JSON.stringify(request)+'\n',yield_time_ms:1000,max_output_tokens:500});
  if(!r.output.includes('response_file'))throw Error('fixture response missing: '+r.output);
  const reply=JSON.parse(await file(root+'/latest.json'));
  events.push({request,status:reply.status,counts:reply.state.counts});return reply;
 };
 const update=async reply=>browser(`async page=>{page.__mcp.state=${JSON.stringify(reply.state)};await page.evaluate(()=>window.__loginSources.filter(s=>!s.closed).forEach(s=>s.listeners.change?.()));return true}`);
 const prepare=async(drop=false)=>{
  const requests=await browser("async page=>{await page.locator('.mcp-login').getByRole('button',{name:'准备认证命令',exact:true}).click();return page.__mcp.pending.map(p=>p.request)}");
  check('single_pending_request_'+events.length,requests.length===1);
  const request=requests[0],reply=await send(request);
  await browser(`async page=>{const p=page.__mcp.pending.shift();${drop?"await p.route.abort('failed');":`page.__mcp.state=${JSON.stringify(reply.state)};await p.route.fulfill({status:${reply.status},json:${JSON.stringify(reply.body)}});`}p.resolve();await page.locator('.mcp-login ${drop||reply.status>=400?'[role=alert]':'pre'}').waitFor();return true}`);
  return {request,reply};
 };
 const select=async(letter,target)=>browser(`async page=>{
  await page.locator('.session-row').filter({has:page.locator('.session-title',{hasText:'OAuth ${letter.toUpperCase()} 受控验证'})}).click();
  await page.locator('.conversation-header h1').filter({hasText:'OAuth ${letter.toUpperCase()} 受控验证'}).waitFor();
  const snapshot=page.locator('.execution-snapshot');if(await snapshot.getAttribute('open')===null)await snapshot.locator(':scope>summary').click();
  const login=page.locator('.mcp-login');if(await login.getAttribute('open')===null)await login.locator('summary').click();
  await page.getByLabel('OAuth 认证目标').selectOption({label:${JSON.stringify(target)}});return true;
 }`);
 const text=async()=>browser("async page=>({error:await page.locator('.mcp-login [role=alert]').allTextContents(),commands:await page.locator('.mcp-login pre').allTextContents()})");
 try{
  const initial=JSON.parse(await file(root+'/ready.json'));
  const assetResult=await tools.exec_command({cmd:"python3 - <<'PY'\nimport json\nfrom pathlib import Path\na={'/'+p.relative_to('web/dist').as_posix():str(p.resolve()) for p in Path('web/dist').rglob('*') if p.is_file()};a['/']=str(Path('web/dist/index.html').resolve());print(json.dumps(a))\nPY",max_output_tokens:700});
  const setup=(await file('/home/zzw/codex-netcap/tests/mcploginui/browser.js')).replace('__FIXTURE__',JSON.stringify(initial)).replace('__ASSETS__',assetResult.output.trim());
  await browser(setup);
  const b=await prepare(true);check('first_receipt_persisted_before_drop',b.reply.status===202&&b.reply.state.counts.login_directories===1);
  check('lost_receipt_no_command_display',(await text()).commands.length===0);
  await select('a','候选快照 · identity');const candidate=await prepare(true);
  check('candidate_has_separate_request',candidate.request.body.command_id!==b.request.body.command_id);
  // Restart actual Application/SQLite, then reload the browser while both IDs are pending.
  const restarted=await send({action:'restart'});await update(restarted);
  check('restart_did_not_recreate_sessions',restarted.state.counts.fixture_creates===2&&restarted.state.counts.operations===2);
  await browser("async page=>{await page.reload();await page.locator('.conversation-header h1').waitFor();return true}");
  await select('b','当前快照 · identity');const retried=await prepare();
  check('retry_same_command_id_after_reload',retried.request.body.command_id===b.request.body.command_id);
  check('retry_same_directory_after_restart',retried.reply.body.mcp_login.directory===b.reply.body.mcp_login.directory);
  check('retry_no_additional_directory',retried.reply.state.counts.login_directories===2);
  const copied=await browser("async page=>{await page.locator('.mcp-login').getByRole('button',{name:'复制认证命令',exact:true}).click();await page.locator('.mcp-login').getByRole('button',{name:'已复制',exact:true}).waitFor();return page.evaluate(()=>window.__copied.at(-1))}");
  check('copied_exact_durable_command',copied===b.reply.body.mcp_login.command);
  check('failed_profile_binding',b.reply.body.mcp_login.profile.endsWith('/profile-b'));
  await select('a','候选快照 · identity');const candidateRetry=await prepare();
  check('return_to_candidate_reuses_its_id',candidateRetry.request.body.command_id===candidate.request.body.command_id);
  check('candidate_directory_preserved',candidateRetry.reply.body.mcp_login.directory===candidate.reply.body.mcp_login.directory);
  check('candidate_bound_to_profile_a',candidateRetry.reply.body.mcp_login.profile.endsWith('/profile-a'));
  await select('a','当前快照 · identity');
  await browser("async page=>{await page.locator('.mcp-login').getByRole('button',{name:'查看已准备命令',exact:true}).click();await page.locator('.mcp-login [role=alert]').waitFor();return true}");
  check('current_cannot_recover_candidate_plan',(await text()).commands.length===0);
  const current=await prepare();
  check('current_snapshot_distinct_from_candidate',current.reply.body.mcp_login.snapshot_id!==candidate.reply.body.mcp_login.snapshot_id);
  check('current_request_distinct_from_candidate',current.request.body.command_id!==candidate.request.body.command_id);
  await select('a','当前快照 · stream');const sse=await prepare();
  check('sse_preparation_supported',sse.reply.status===202&&sse.reply.body.mcp_login.server==='stream');
  for(const target of ['local','helper']){
   await select('a','当前快照 · '+target);const rejected=await prepare();
   check(target+'_rejected',rejected.reply.status===409);
   check(target+'_no_directory',rejected.reply.state.counts.login_directories===4);
   check(target+'_no_stale_command',(await text()).commands.length===0);
  }
  await select('a','当前快照 · identity');
  await browser("async page=>{await page.locator('.mcp-login').getByRole('button',{name:'查看已准备命令',exact:true}).click();await page.locator('.mcp-login pre').waitFor();return true}");
  check('recover_current_exact_command',(await text()).commands[0]===current.reply.body.mcp_login.command);
  const source=await browser("async page=>({text:await page.locator('.execution-snapshot').innerText(),error:page.__mcp.errors})");
  check('native_sources_shown',['原生来源：本次选择','原生来源：用户配置','原生来源：CLI 插件'].every(s=>source.text.includes(s)));
  check('auth_status_not_verified',source.text.includes('identity · 需要认证 · 未核对一致'));
  check('inventory_not_permission',source.text.includes('清单不代表调用已获准'));
  check('historical_use_separate',source.text.includes('历史成功调用：past_call'));
  for(const width of [1440,768,390]){
   const visible=await browser(`async page=>{await page.setViewportSize({width:${width},height:1050});if(${width}<1200&&!await page.locator('.inspector').isVisible())await page.getByRole('button',{name:'详情',exact:true}).click();await page.locator('.mcp-login').scrollIntoViewIfNeeded();await page.screenshot({path:${JSON.stringify('/home/zzw/codex-netcap/.dev/w0206-auth-ui/browser-') }+${width}+'.png',fullPage:true});return {overflow:await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),visible:await page.locator('.mcp-login pre').isVisible()}}`);
   check('no_page_overflow_'+width,!visible.overflow);check('command_visible_'+width,visible.visible);
  }
  await browser("async page=>{await page.setViewportSize({width:1440,height:1050});await page.evaluate(()=>dispatchEvent(new Event('offline')));return true}");
  check('offline_prepare_and_recover_disabled',await browser("async page=>await page.locator('.mcp-login').getByRole('button',{name:'准备认证命令',exact:true}).isDisabled()&&await page.locator('.mcp-login').getByRole('button',{name:'查看已准备命令',exact:true}).isDisabled()"));
  await browser("async page=>{await page.evaluate(()=>dispatchEvent(new Event('online')));await page.locator('.mcp-login').getByRole('button',{name:'准备认证命令',exact:true}).waitFor();return true}");
  const drift=await send({action:'drift'});await update(drift);const refused=await prepare();
  check('drift_rejected',refused.reply.status===409&&refused.reply.body.error.includes('来源'));
  check('failed_reprepare_clears_old_command',(await text()).commands.length===0);
  await browser("async page=>{await page.getByRole('button',{name:'核对当前来源',exact:true}).click();await page.getByText('来源或绑定已变化，继续前需核对。',{exact:true}).waitFor();return true}");
  check('source_drift_visible',true);
  check('no_native_input_or_new_operations',refused.reply.state.counts.native_inputs===0&&refused.reply.state.counts.operations===2&&refused.reply.state.counts.fixture_creates===2);
  check('exactly_four_successful_preparations',refused.reply.state.counts.login_directories===4);
  const final=await browser("async page=>({errors:page.__mcp.errors,requests:page.__mcp.requests})");
  check('no_javascript_errors',final.errors.length===0);
  check('browser_writes_only_preparation',final.requests.filter(r=>r.method==='POST').every(r=>r.path==='/api/commands'&&r.body.kind==='prepare_mcp_login'));
  store('authUIResult',{checks,events,requests:final.requests,scope:'actual browser POST payloads through live Application/SQLite/Handler via tool mediation; controlled runtime/GET exports/EventSource/clipboard; no native OAuth or HTTP listener'});
 }catch(error){store('authUIResult',{checks,events,error:String(error)});throw error;}
 return load('authUIResult');
})()
