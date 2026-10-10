import assert from 'node:assert/strict';
import { verifyMCP } from '../bridges/claude/dist/mcp.js';
const selections=[{asset_id:'asset',server:'identity',mode:'enabled',source_path:'/fixture/mcp.json'}];
const native={mcpServerStatus:async()=>[{name:'identity',status:'needs-auth',source:'dynamic',error:'sensitive-error',config:{headers:{Authorization:'sensitive-token'}}}]};
await assert.rejects(verifyMCP(native,selections),error=>error.message.includes('identity')&&error.message.includes('OAuth')&&!error.message.includes('sensitive'));
await verifyMCP(native,[]);
await verifyMCP({mcpServerStatus:async()=>[{name:'identity',status:'connected',source:'dynamic',tools:[]}]},selections);
console.log('PASS OAuth diagnostic, unselected server and authenticated selection');
