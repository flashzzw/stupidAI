import assert from 'node:assert/strict';
import { verifyMCP } from '../bridges/claude/dist/mcp.js';
const enabled=[{asset_id:'shared',server:'identity',mode:'enabled',source_path:'/fixture/mcp.json'}];
const check=async (selection,statuses,pattern)=>{
  await assert.rejects(verifyMCP({mcpServerStatus:async()=>statuses},selection),error=>pattern.test(error.message)&&!error.message.includes('private-value'));
};
await check(enabled,[],/identity.*未出现在/);
await check([{...enabled[0],mode:'disabled'}],[{name:'identity',status:'connected',source:'local',tools:[]}],/identity.*local.*禁用/);
await check(enabled,[{name:'identity',status:'connected',source:'project',tools:[]}],/identity.*project.*不一致/);
await check(enabled,[{name:'identity',status:'failed',source:'dynamic',error:'private-value',config:{env:{KEY:'private-value'}}}],/identity.*连接失败/);
await check(enabled,[{name:'identity',status:'connected',source:'dynamic'},{name:'identity',status:'connected',source:'user'}],/identity.*重复/);
await check(enabled,[{name:'identity',status:'connected',source:'private-value'}],/identity.*未知来源/);
await assert.rejects(verifyMCP({mcpServerStatus:async()=>{throw Error('private-value');}},enabled),error=>/清单不可读取/.test(error.message)&&!error.message.includes('private-value'));
console.log('PASS 7 selected-server diagnostics, source conflicts and redaction');
