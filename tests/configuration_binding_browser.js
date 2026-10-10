// Real Handler snapshots across source drift, native restart and restoration.
async page => {
 const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
 const first=fixture.responses.initial,path=Object.keys(first).find(p=>p.endsWith('/execution')),sid=first[path].snapshot.session_id;
 let phase='initial';const check=(name,value)=>{checks[name]=!!value;if(!value)throw Error(name);};
 page.setDefaultTimeout(6000);await page.unrouteAll({behavior:'ignoreErrors'});
 await page.addInitScript(id=>{sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};},sid);
 page.on('pageerror',e=>errors.push(e.message));
 await page.route('**/*',async route=>{const req=route.request(),p=req.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];requests.push({path:p,method:req.method()});if(fixture.responses[phase]?.[p])return route.fulfill({json:fixture.responses[phase][p]});if(assets[p])return route.fulfill({path:assets[p],contentType:p.endsWith('.js')?'application/javascript':p.endsWith('.css')?'text/css':'text/html'});if(p.startsWith('/favicon'))return route.fulfill({status:204,body:''});return route.fulfill({status:409,body:'No mutation permitted'});});
 await page.setViewportSize({width:1440,height:1000});await page.goto('http://127.0.0.1:9463/');
 const inspect=async(label,drift)=>{
  const snapshot=page.locator('.execution-snapshot');await snapshot.locator(':scope > summary').click();
  const saved=page.locator('.configuration-source-snapshot');await saved.locator(':scope > summary').click();
  check(label+'_saved_layers_visible',(await saved.innerText()).includes('已保存 3 个原生配置层'));
  check(label+'_old_snapshot_id',(await snapshot.innerText()).includes(first[path].snapshot.id));
  check(label+'_saved_permission_not_rewritten',(await saved.innerText()).includes('文件权限 600')&&!(await saved.innerText()).includes('文件权限 640'));
  const source=page.locator('.execution-source-check');await source.getByRole('button',{name:'核对当前来源',exact:true}).click();
  await source.getByText(drift?'来源或绑定已变化，继续前需核对。':'已记录的来源与绑定未发现变化。',{exact:true}).waitFor();
  const coverage=source.getByRole('region',{name:'本次来源检查覆盖'});
  check(label+'_source_coverage_recorded',await coverage.getByText('Codex 磁盘合并来源版本 · 已记录',{exact:true}).isVisible());
  if(drift){
   check(label+'_specific_config_drift',(await coverage.innerText()).includes('Codex 配置来源文件的内容、权限、路径或存在状态已变化'));
   await page.getByRole('textbox',{name:'消息',exact:true}).fill('MUST NOT SEND');
   check(label+'_send_blocked',await page.getByRole('button',{name:'发送消息',exact:true}).isDisabled());
  }
 };
 await inspect('initial',false);
 for(const next of ['drift','reopened','restored']){phase=next;await page.reload();await inspect(next,next!=='restored');}
 phase='reopened';await page.reload();await inspect('mobile_base',true);
 await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'详情',exact:true}).click();await page.locator('.execution-source-check').scrollIntoViewIfNeeded();
 check('mobile_no_overflow',await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 await page.screenshot({path:'.dev/w0209-config-binding/reopened-390.png',fullPage:true});
 check('only_read_requests',requests.every(r=>r.method==='GET'));check('no_page_errors',errors.length===0);
 return {checks,passed:Object.values(checks).every(Boolean),requests,errors,scope:'actual bound-source snapshot before/after native restart; source file drift and restored original version'};
}
