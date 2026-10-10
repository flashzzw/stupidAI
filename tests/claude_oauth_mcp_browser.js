// Authenticated claude_oauth_mcp_smoke.py --hold fixture; only profile A logged in.
async page => {
  const checks={},errors=[];const onError=e=>errors.push(String(e));page.on('pageerror',onError);
  const check=(name,value)=>{checks[name]=Boolean(value);if(!value)throw Error(name);};
  const snapshot=()=>page.evaluate(async ()=>(await fetch('/api/snapshot')).json());
  const until=async predicate=>{const deadline=Date.now()+60000;while(!await predicate()){if(Date.now()>deadline)throw Error('state did not settle');await page.waitForTimeout(100);}};
  async function create(letter,mode){
    await page.getByRole('button',{name:'＋ 新建',exact:true}).click();
    await page.getByLabel('会话名称',{exact:true}).fill(`OAuth ${letter} ${mode}`);
    await page.locator('select[name="project"]').selectOption('claude-'+letter.toLowerCase());
    await page.locator('select[name="route"]').selectOption('claude-'+letter.toLowerCase()+'-route');
    await page.locator('select[name="capability_selection:shared"]').selectOption(mode);
    const creating=page.waitForResponse(r=>r.url().endsWith('/api/commands')&&r.request().postDataJSON()?.kind==='create');
    await page.getByRole('button',{name:'创建会话',exact:true}).click();
    return (await (await creating).json()).session_id;
  }
  try{
    await page.setViewportSize({width:1440,height:1050});
    const a=await create('A','enabled');
    await until(async ()=>(await snapshot()).sessions.find(s=>s.id===a)?.status==='idle');
    await page.locator('.execution-snapshot > summary').click();
    await page.getByText('identity · 已连接 · 与选择一致',{exact:true}).waitFor();
    check('native_oauth_scope_is_explained',await page.getByText(/OAuth 登录命令可在下方准备，原生 CLI 负责授权回调/).isVisible());
    const native=(await snapshot()).sessions.find(s=>s.id===a).scope.native_id;
    await page.getByRole('textbox',{name:'消息',exact:true}).fill('NETCAP_A_TURN_1');
    await page.getByRole('button',{name:'发送消息',exact:true}).click();
    await page.getByRole('button',{name:'允许本次',exact:true}).waitFor();
    check('oauth_does_not_grant_tool_permission',await page.getByRole('button',{name:'允许此会话',exact:true}).count()===0);
    await page.getByRole('button',{name:'允许本次',exact:true}).click();
    await page.getByText('历史成功调用：whoami',{exact:true}).waitFor();
    check('original_id_after_oauth_call',(await snapshot()).sessions.find(s=>s.id===a).scope.native_id===native);
    await page.screenshot({path:'.dev/w0206-auth/browser-connected-1440.png'});
    const b=await create('B','enabled');
    await until(async ()=>(await snapshot()).operations.some(o=>o.session_id===b&&o.intent.kind==='create'&&o.status==='not_sent'));
    const failed=(await snapshot()).sessions.find(s=>s.id===b);
    check('other_profile_remains_unauthenticated',failed.status==='closed'&&!failed.scope.native_id);
    const operation=page.locator('.operation').filter({hasText:'create · 确认未发送'});
    await operation.locator('summary').click();
    await operation.getByText(/MCP identity 需要 OAuth 认证/).first().waitFor();
    check('oauth_failure_names_selected_server',await operation.getByText(/MCP identity 需要 OAuth 认证/).first().isVisible());
    const oldCount=(await snapshot()).operations.length;
    await page.locator('.execution-snapshot > summary').click();
    const login=page.locator('.mcp-login');
    await login.locator('summary').click();
    const preparing=page.waitForResponse(r=>r.url().endsWith('/api/commands')&&r.request().postDataJSON()?.kind==='prepare_mcp_login');
    await login.getByRole('button',{name:'准备认证命令',exact:true}).click();
    const plan=(await (await preparing).json()).mcp_login;
    await login.getByRole('button',{name:'复制认证命令',exact:true}).waitFor();
    check('preparation_is_bound_to_failed_profile',plan.snapshot_id===failed.execution_snapshot.id&&plan.profile===failed.scope.instance.profile&&plan.server==='identity');
    check('preparation_does_not_start_native_operation',(await snapshot()).operations.length===oldCount);
    check('command_uses_native_headless_login',plan.command.includes('mcp login')&&plan.command.includes('--no-browser'));
    await login.getByRole('button',{name:'复制认证命令',exact:true}).click();
    await login.getByRole('button',{name:'已复制',exact:true}).waitFor();
    check('copy_prepared_command',true);
    await page.reload();
    await until(async ()=>(await snapshot()).sessions.find(s=>s.id===b)?.status==='closed');
    check('reload_does_not_retry_auth_or_create',(await snapshot()).operations.length===oldCount);
    await page.locator('.execution-snapshot > summary').click();
    await login.locator('summary').click();
    await login.getByRole('button',{name:'查看已准备命令',exact:true}).click();
    await login.getByRole('button',{name:'复制认证命令',exact:true}).waitFor();
    check('prepared_command_survives_reload',await login.locator('pre').textContent()===plan.command);
    await page.locator('.operation').filter({hasText:'create · 确认未发送'}).locator('summary').click();
    for(const width of [1440,768]){
      await page.setViewportSize({width,height:1050});
      if(width===768)await page.getByRole('button',{name:'详情',exact:true}).click();
      await page.locator('.operation').filter({hasText:'create · 确认未发送'}).scrollIntoViewIfNeeded();
      check('layout_'+width,await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
      await page.screenshot({path:`.dev/w0206-auth/browser-needs-auth-${width}.png`});
    }
    check('no_javascript_errors',errors.length===0);
    return {checks,errors,expectations_met:Object.values(checks).every(Boolean),sessions:{A:a,B:b},native_id:native};
  }finally{page.off('pageerror',onError);}
}
