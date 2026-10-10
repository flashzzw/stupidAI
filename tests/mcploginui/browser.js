// Render current exports; queue writes for the live JSON-line Handler fixture.
// Inject the runner export and built asset map into the constants below.
async page => {
 const fixture=__FIXTURE__,assets=__ASSETS__;
 page.__mcp={state:fixture,pending:[],requests:[],errors:[]};
 page.on('pageerror',e=>page.__mcp.errors.push(e.message));
 await page.addInitScript(id=>{
  if(!sessionStorage.getItem('netcap.selected.v1'))sessionStorage.setItem('netcap.selected.v1',id);
  window.__loginSources=[];window.__copied=[];
  window.EventSource=class{constructor(){this.listeners={};window.__loginSources.push(this);setTimeout(()=>this.onopen?.(),0)}addEventListener(n,f){this.listeners[n]=f}close(){this.closed=true}};
  Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async text=>{window.__copied.push(text)}}});
 },fixture.sessions.b);
 await page.route('**/*',async route=>{
  const r=route.request(),path=r.url().replace(/^https?:\/\/[^/]+/,'').split('?')[0];
  const request={method:r.method(),path,body:r.postData()?JSON.parse(r.postData()):null};
  page.__mcp.requests.push(request);
  if(path.startsWith('/api/')&&r.method()==='POST'){
   await new Promise(resolve=>page.__mcp.pending.push({request,route,resolve}));return;
  }
  if(page.__mcp.state.responses[path])return route.fulfill({json:page.__mcp.state.responses[path]});
  if(assets[path])return route.fulfill({path:assets[path],contentType:path.endsWith('.js')?'application/javascript':path.endsWith('.css')?'text/css':path.endsWith('.svg')?'image/svg+xml':'text/html'});
  return route.fulfill({status:404,body:'unmapped fixture route'});
 });
 await page.setViewportSize({width:1440,height:1000});await page.goto('http://127.0.0.1:9453/');
 await page.locator('.execution-snapshot>summary').click();await page.locator('.mcp-login>summary').click();
 await page.getByLabel('OAuth 认证目标').selectOption({label:'当前快照 · identity'});
 return {ready:true,counts:fixture.counts,sessions:fixture.sessions};
}
