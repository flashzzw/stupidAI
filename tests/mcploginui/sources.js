// Presentation-only variants of the live fixture export; restore it afterwards.
async page => {
 const saved=JSON.parse(JSON.stringify(page.__mcp.state)),checks={};
 const sid=saved.sessions.a,path='/api/sessions/'+sid;
 const before=page.__mcp.requests.filter(r=>r.method==='POST').length;
 const check=(n,v)=>{checks[n]=!!v;if(!v)throw Error(n)};
 const item=page.locator('.snapshot-asset').filter({has:page.locator('p').filter({hasText:/^identity ·/})});
 let sequence=page.__mcp.state.responses['/api/workspace'].sequence;
 async function refresh(source){
  const data=JSON.parse(JSON.stringify(saved));
  data.responses['/api/workspace'].sequence=++sequence;
  data.responses[path].sequence=sequence;
  const evidence=data.responses[path].session.view.capability_evidence[0];
  if(source===undefined)delete evidence.source;else evidence.source=source;
  page.__mcp.state=data;
  await page.evaluate(()=>window.__loginSources.filter(s=>!s.closed).forEach(s=>s.listeners.change?.()));
 }
 try{
  for(const [source,label] of Object.entries({dynamic:'本次选择',user:'用户配置',project:'项目配置',local:'项目本地配置',managed:'受管配置',enterprise:'企业配置',plugin:'CLI 插件',claudeai:'Claude 账号连接',agent:'子代理配置',sdk:'SDK 内部服务',unknown:'未知来源','future-fixture-source':'未知来源'})){
   await refresh(source);await item.getByText('原生来源：'+label,{exact:true}).waitFor();
   check(source+'_label',true);
   check(source+'_does_not_grant_authority',(await item.innerText()).includes('需要认证 · 未核对一致')&&(await item.innerText()).includes('清单不代表调用已获准'));
   if(source==='future-fixture-source')check('unknown_raw_source_hidden',!(await item.innerText()).includes(source));
  }
  await refresh(undefined);await item.getByText(/^原生来源：/).waitFor({state:'detached'});
  check('legacy_source_not_inferred',await item.getByText(/^原生来源：/).count()===0);
  check('legacy_status_retained',(await item.innerText()).includes('需要认证 · 未核对一致'));
  await refresh('dynamic');await item.getByText('原生来源：本次选择',{exact:true}).waitFor();
  for(const width of [1440,768,390]){
   await page.setViewportSize({width,height:1050});
   if(width<1200&&!await page.locator('.inspector').isVisible())await page.getByRole('button',{name:'详情',exact:true}).click();
   await item.scrollIntoViewIfNeeded();await page.screenshot({path:'/home/zzw/codex-netcap/.dev/w0206-auth-ui/sources-'+width+'.png',fullPage:true});
   check('sources_no_horizontal_overflow_'+width,await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  }
  check('source_variants_no_write_requests',page.__mcp.requests.filter(r=>r.method==='POST').length===before);
  check('no_javascript_errors',page.__mcp.errors.length===0);
  return {checks,scope:'controlled presentation variants of Handler export; source labels and missing-field compatibility only, no native source/permission validation'};
 }finally{
  saved.responses['/api/workspace'].sequence=++sequence;saved.responses[path].sequence=sequence;
  page.__mcp.state=saved;await page.evaluate(()=>window.__loginSources.filter(s=>!s.closed).forEach(s=>s.listeners.change?.()));
 }
}
