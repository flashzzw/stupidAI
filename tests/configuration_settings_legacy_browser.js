// Synthetic older/unsupported records derived from real Handler responses.
// The Application's legacy comparison behavior is checked separately in Go.
async page => {
 const fixture=__FIXTURE__,assets=__ASSETS__,checks={},requests=[],errors=[];
 const rows=JSON.parse(JSON.stringify(fixture.responses.initial));
 const path=Object.keys(rows).find(p=>p.endsWith('/configuration/catalog')),sid=path.split('/')[3];
 const strip=value=>{if(!value||typeof value!=='object')return;for(const [key,item] of Object.entries(value)){if(key==='codex_configuration_sources'&&item)delete item.settings;else strip(item);}};
 strip(rows);delete rows[path].settings;
 let mode='unrecorded';const check=(name,value)=>{checks[name]=!!value;if(!value)throw Error(name);};
 page.setDefaultTimeout(6000);await page.unrouteAll({behavior:'ignoreErrors'});
 await page.addInitScript(id=>{sessionStorage.clear();sessionStorage.setItem('netcap.selected.v1',id);window.EventSource=class{constructor(){setTimeout(()=>this.onopen?.(),0);}addEventListener(){}close(){}};},sid);
 page.on('pageerror',e=>errors.push(e.message));
 await page.route('**/*',async route=>{
  const req=route.request(),p=req.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];requests.push({path:p,method:req.method(),body:req.postData()});
  if(rows[p]){
   const data=JSON.parse(JSON.stringify(rows[p]));
   if(p===path){if(mode==='missing')delete data.settings_difference;else data.settings_difference={status:mode,base_snapshot_id:rows[path].settings_difference.base_snapshot_id};}
   return route.fulfill({json:data});
  }
  if(assets[p])return route.fulfill({path:assets[p],contentType:p.endsWith('.js')?'application/javascript':p.endsWith('.css')?'text/css':'text/html'});
  return route.fulfill({status:p.startsWith('/favicon')?204:409,body:''});
 });
 await page.setViewportSize({width:1440,height:1000});
 for(const sample of ['unrecorded','missing','unsupported']){
  mode=sample;await page.goto('http://127.0.0.1:9463/');
  await page.locator('.execution-snapshot > summary').click();
  const saved=page.locator('.configuration-source-snapshot');await saved.locator(':scope > summary').click();
  check(sample+'_saved_evidence_gap',(await saved.innerText()).includes('此版本未记录可用的设置比较依据'));
  const catalog=page.locator('.configuration-catalog');await catalog.locator(':scope > summary').click();
  await catalog.getByRole('button',{name:'读取当前配置来源',exact:true}).click();await catalog.getByRole('status').waitFor();
  const diff=catalog.getByRole('region',{name:'与执行快照设置比较'}),text=await diff.innerText();
  check(sample+'_difference_gap',text.includes(sample==='unsupported'?'设置比较依据的版本或格式尚未核对':'不能补造原值'));
  check(sample+'_no_invented_values',!text.includes('→')&&!text.includes('其他合并设置：未变化'));
  check(sample+'_read_not_application',text.includes('不代表线程已采用新设置'));
 }
 check('read_only_requests',requests.every(r=>r.method==='GET'||r.path===path&&r.method==='POST'&&r.body==='{}'));
 check('no_page_errors',errors.length===0);
 return {checks,passed:Object.values(checks).every(Boolean),requests,errors,scope:'synthetic missing/legacy/unsupported settings evidence display; no runtime mutation'};
}
