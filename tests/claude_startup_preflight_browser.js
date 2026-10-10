// Actual Application/Handler reads; helper display uses the controlled SDK case.
async page=>{
 const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
 let phase='valid',failed=false;
 const sid=fixture.sessionId,path='/api/sessions/'+sid+'/plugins/startup-settings';
 const check=(name,ok)=>{checks[name]=!!ok;if(!ok)throw Error(name);};
 page.setDefaultTimeout(6000);await page.unrouteAll({behavior:'ignoreErrors'});page.on('pageerror',e=>errors.push(e.message));
 await page.addInitScript(id=>{sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){window.__startupSourceStream=this;setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};},sid);
 await page.route('**/*',async route=>{
  const r=route.request(),p=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];requests.push({path:p,method:r.method(),body:r.postData()});
  if(p===path)return failed?route.fulfill({status:409,json:{error:'来源读取期间会话绑定已变化，请重新读取'}}):route.fulfill({json:fixture.reports[phase]});
  if(fixture.responses[p])return route.fulfill({json:fixture.responses[p]});
  if(assets[p])return route.fulfill({path:assets[p],contentType:p.endsWith('.js')?'application/javascript':p.endsWith('.css')?'text/css':'text/html'});
  return route.fulfill({status:409,body:'No execution in metadata replay'});
 });
 await page.setViewportSize({width:1440,height:1050});await page.goto('http://127.0.0.1:9463/');
 await page.locator('.execution-snapshot>summary').click();await page.getByText('Claude 已安装插件登记',{exact:true}).click();
 const panel=page.locator('.startup-settings');await panel.locator(':scope>summary').click();
 check('no_automatic_source_read',!requests.some(r=>r.path===path));
 await panel.getByRole('button',{name:'读取设置来源',exact:true}).click();await panel.getByRole('status').waitFor();
 check('complete_read_is_not_admission',(await panel.innerText()).includes('本次来源读取完成；完整启动配置仍待核对。'));
 check('sdk_local_difference_explicit',(await panel.innerText()).includes('预解析与原生 Git/cwd 来源可能不同'));
 check('remote_policy_gap_explicit',(await panel.innerText()).includes('未获取最新服务端策略'));
 const first=requests.find(r=>r.path===path);check('only_empty_bound_request',first.method==='POST'&&first.body==='{}');
 const report=fixture.reports.valid;
 const content=await panel.innerText();check('observed_file_hashes_visible',report.files.filter(f=>f.sha256).every(f=>content.includes(f.path)&&content.includes(f.sha256)));
 check('registry_hives_visible',(await panel.innerText()).includes('HKLM')&&(await panel.innerText()).includes('HKCU'));
 await panel.getByText('原生 Git/cwd 对应的本地文件',{exact:true}).click();
 check('primary_file_not_hidden',(await panel.innerText()).includes(report.bound_local_files.at(-1).path));
 check('no_settings_values_or_commands',(await panel.innerText()).includes('文件正文与策略值不在此展示')&&!(await panel.innerText()).includes('PRIVATE_PREFLIGHT_SENTINEL')&&!(await panel.innerText()).includes('printf ran'));
 await panel.scrollIntoViewIfNeeded();await page.screenshot({path:'.dev/w0208-startup-preflight/valid-1440.png',fullPage:true});
 phase='malformed';await panel.getByRole('button',{name:'读取设置来源',exact:true}).click();await panel.getByText('本次来源读取不完整；完整启动配置仍待核对。',{exact:true}).waitFor();
 check('invalid_json_explicit',(await panel.innerText()).includes('JSON 无效'));
 failed=true;await panel.getByRole('button',{name:'读取设置来源',exact:true}).click();await panel.getByRole('alert').waitFor();
 check('failed_refresh_clears_previous_read',await panel.getByRole('status').count()===0&&!(await panel.innerText()).includes('JSON 无效'));
 failed=false;phase='helper';await panel.getByRole('button',{name:'读取设置来源',exact:true}).click();await panel.getByRole('status').waitFor();
 check('helper_not_run_explicit',(await panel.innerText()).includes('来源声明了策略辅助程序，但本次没有执行；其最终策略仍未知'));
 await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await panel.scrollIntoViewIfNeeded();
 check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:'.dev/w0208-startup-preflight/helper-390.png',fullPage:true});
 await page.evaluate(()=>window.__startupSourceStream.onerror?.(new Event('error')));await page.getByText('连接断开 · 数据已过期',{exact:true}).waitFor();
 await page.getByText('Claude 已安装插件登记',{exact:true}).click();await page.locator('.startup-settings>summary').click();
 check('disconnected_read_disabled',await page.locator('.startup-settings').getByRole('button',{name:'读取设置来源',exact:true}).isDisabled());
 check('no_execution_command',!requests.some(r=>r.path==='/api/commands'));
 check('no_page_errors',errors.length===0);
 return{checks,passed:Object.values(checks).every(Boolean),requests,errors,scope:'real Handler valid/malformed metadata reads, controlled SDK helper result and browser refresh/disconnect; no native commands'};
}
