// Production Application rejection response; HTTP/SSE are replayed, no native launch.
async page => {
  const fixture=__FIXTURE__,assets=__ASSETS__,output=__OUTPUT__,checks={},requests=[],errors=[];
  const sid=fixture.session_id,base='/api/sessions/'+sid,key=fixture.preview.setting.source_key;
  const check=(name,value)=>{checks[name]=!!value;if(!value)throw Error(name);};
  page.setDefaultTimeout(6000);await page.unrouteAll({behavior:'ignoreErrors'});page.on('pageerror',error=>errors.push(error.message));
  await page.addInitScript(id=>{
    sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);
    window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};
  },sid);
  await page.route('**/*',async route=>{
    const req=route.request(),path=req.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];
    requests.push({path,method:req.method(),body:req.postData()?JSON.parse(req.postData()):null});
    if(path===base+'/plugins/installed')return route.fulfill({json:fixture.catalog});
    if(path===base+'/plugins/settings-preview')return route.fulfill({json:fixture.preview});
    if(fixture.responses[path])return route.fulfill({json:fixture.responses[path]});
    if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':'text/html'});
    return route.fulfill({status:409,body:'Unmapped fixture endpoint'});
  });
  await page.setViewportSize({width:1440,height:1050});await page.goto('http://127.0.0.1:9463/');
  await page.locator('.execution-snapshot>summary').click();
  const panel=page.locator('details.skill-discovery').filter({has:page.getByText('Claude 已安装插件登记',{exact:true})});
  await panel.locator(':scope>summary').click();
  await panel.getByRole('button',{name:'读取安装登记',exact:true}).click();
  await panel.getByText('支持范围内的登记采样完成 · 1 条安装记录',{exact:true}).waitFor();
  check('catalog_remains_available',await panel.getByRole('button',{name:key+'：核对排除设置',exact:true}).isEnabled());
  const preview=panel.locator('.snapshot-asset>.startup-settings');
  await preview.getByRole('button',{name:key+'：核对排除设置',exact:true}).click();
  await preview.getByText('预览未完成，请查看诊断。',{exact:true}).waitFor();
  const text=await preview.innerText();
  check('specific_startup_conflict_visible',text.includes('apiKeyHelper')&&text.includes('凭据引用冲突'));
  check('original_file_path_visible',text.includes(fixture.responses[base].session.scope.instance.settings_file));
  check('no_candidate_or_effective_value_claim',text.includes('未生成临时设置文件')&&!text.includes('原生观察值：')&&!text.includes('目标设置核对相符'));
  check('no_helper_or_credential_value_leaked',!text.includes('PRIVATE_API_HELPER')&&!text.includes('PRIVATE_ROUTE_VALUE'));
  check('full_admission_still_unavailable',text.includes('正式启停尚未开放'));
  check('catalog_can_be_read_again',await panel.getByRole('button',{name:'读取安装登记',exact:true}).isEnabled());
  const request=requests.find(r=>r.path===base+'/plugins/settings-preview');
  check('original_source_key_and_version_sent',request?.body.source_key===key&&request?.body.source_version===fixture.catalog.source_version&&request?.body.mode==='disabled');
  check('no_execution_command',!requests.some(r=>r.path==='/api/commands'));
  check('no_page_error',errors.length===0);
  await panel.screenshot({path:output+'/startup-conflict.png'});
  return{checks,requests,errors,passed:Object.values(checks).every(Boolean),scope:fixture.scope+'; browser display only'};
}
