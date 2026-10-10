// Current SDK registry reports in a saved Application envelope; HTTP/SSE are replayed.
async page => {
  const fixture=__FIXTURE__,assets=__ASSETS__,output=__OUTPUT__,checks={},requests=[],errors=[];
  let phase='registry-malformed',failed=false;
  const path='/api/sessions/'+fixture.sessionId+'/plugins/startup-settings';
  const check=(name,value)=>{checks[name]=!!value;if(!value)throw Error(name);};
  page.setDefaultTimeout(6000);
  await page.unrouteAll({behavior:'ignoreErrors'});
  page.on('pageerror',error=>errors.push(error.message));
  await page.addInitScript(id=>{
    sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);
    window.EventSource=class{
      constructor(){window.__registrySourceStream=this;setTimeout(()=>this.onopen?.(),0);}
      addEventListener(){}close(){}
    };
  },fixture.sessionId);
  await page.route('**/*',async route=>{
    const r=route.request(),p=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];
    requests.push({path:p,method:r.method(),body:r.postData()});
    if(p===path)return failed
      ?route.fulfill({status:409,json:{error:'来源读取期间会话绑定已变化，请重新读取'}})
      :route.fulfill({json:fixture.reports[phase]});
    if(fixture.responses[p])return route.fulfill({json:fixture.responses[p]});
    if(assets[p])return route.fulfill({path:assets[p],contentType:p.endsWith('.js')?'application/javascript':p.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:409,body:'No execution in registry metadata replay'});
  });
  await page.setViewportSize({width:1440,height:1050});
  await page.goto('http://127.0.0.1:9463/');
  await page.locator('.execution-snapshot>summary').click();
  await page.getByText('Claude 已安装插件登记',{exact:true}).click();
  const panel=page.locator('.startup-settings');
  await panel.locator(':scope>summary').click();
  check('no_automatic_source_read',!requests.some(r=>r.path===path));
  const sample=async mode=>{
    phase=mode;
    await panel.getByRole('button',{name:'读取设置来源',exact:true}).click();
    await panel.getByRole('status').waitFor();
    return panel.innerText();
  };
  let content=await sample('registry-malformed');
  check('malformed_registry_incomplete',content.includes('本次来源读取不完整；完整启动配置仍待核对。'));
  check('both_malformed_hives_identified',content.includes('HKLM · 设置 JSON 无效')&&content.includes('HKCU · 设置 JSON 无效'));
  check('no_process_failure_invented',!content.includes('返回码'));
  content=await sample('registry-empty-success');
  check('empty_success_not_read',content.includes('本次来源读取不完整')&&content.includes('HKLM · 未读到有效的 Settings 值')&&content.includes('HKCU · 未读到有效的 Settings 值'));
  check('refresh_replaces_previous_diagnostic',!content.includes('设置 JSON 无效'));
  content=await sample('registry-hkcu-malformed');
  check('valid_hklm_does_not_hide_invalid_hkcu',content.includes('HKLM · 已读取来源')&&content.includes('HKCU · 设置 JSON 无效')&&content.includes('本次来源读取不完整'));
  check('private_registry_values_not_rendered',!content.includes('PRIVATE_PREFLIGHT_SENTINEL'));
  check('policy_and_permission_gaps_remain',content.includes('未获取最新服务端策略')&&content.includes('不能据此确认完整权限或允许已安装插件启停'));
  const registry=panel.locator('.snapshot-asset').filter({has:page.getByText('Windows 策略来源',{exact:true})});
  await registry.scrollIntoViewIfNeeded();
  await registry.screenshot({path:output+'/registry-values-1440.png'});
  content=await sample('registry-empty-object');
  check('empty_json_object_is_readable',content.includes('本次来源读取完成；完整启动配置仍待核对。')&&!content.includes('设置 JSON 无效')&&!content.includes('未读到有效的 Settings 值'));
  content=await sample('registry-expand');
  check('expand_string_source_is_readable',content.includes('本次来源读取完成')&&content.includes('HKLM · 已读取来源'));
  content=await sample('registry-denied');
  check('existing_denied_read_stays_unknown',content.includes('本次来源读取不完整')&&content.includes('HKLM · 读取结果未知'));
  failed=true;
  await panel.getByRole('button',{name:'读取设置来源',exact:true}).click();
  await panel.getByRole('alert').waitFor();
  check('failed_refresh_clears_prior_report',await panel.getByRole('status').count()===0&&!((await panel.innerText()).includes('Windows 策略来源')));
  failed=false;await sample('registry-malformed');
  await page.setViewportSize({width:390,height:844});
  await page.getByRole('button',{name:'详情',exact:true}).click();
  await panel.scrollIntoViewIfNeeded();
  check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await registry.scrollIntoViewIfNeeded();
  await registry.screenshot({path:output+'/registry-values-390.png'});
  await page.evaluate(()=>window.__registrySourceStream.onerror?.(new Event('error')));
  await page.getByText('连接断开 · 数据已过期',{exact:true}).waitFor();
  await page.getByText('Claude 已安装插件登记',{exact:true}).click();
  await page.locator('.startup-settings>summary').click();
  check('disconnected_read_disabled',await page.locator('.startup-settings').getByRole('button',{name:'读取设置来源',exact:true}).isDisabled());
  check('only_bound_empty_reads',requests.filter(r=>r.path===path).every(r=>r.method==='POST'&&r.body==='{}'));
  check('no_execution_command',!requests.some(r=>r.path==='/api/commands'));
  check('no_page_errors',errors.length===0);
  return {checks,requests,errors,passed:Object.values(checks).every(Boolean),scope:'current real SDK registry metadata with controlled OS replies; saved Application envelope and HTTP/SSE replay; no native query or live Windows policy'};
}
