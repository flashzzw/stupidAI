import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { mcpDefinitions, mcpInventory } from '../bridges/claude/dist/mcp.js';

const root=mkdtempSync(join(tmpdir(),'netcap-helper-definition-'));
const source=join(root,'mcp.json'), marker=join(root,'must-not-run');
const command=`printf invoked > '${marker}'; auth-tool --profile "$ACCOUNT"`;
const selection={asset_id:'shared',server:'identity',mode:'enabled',source_path:source};
try {
  for (const type of ['http','sse']) {
    writeFileSync(source,JSON.stringify({mcpServers:{identity:{type,url:'https://example.invalid/mcp',headers:{'X-Account':'${ACCOUNT}'},headersHelper:command}}}));
    const servers=mcpDefinitions([selection]);
    assert.equal(servers.identity.headersHelper,command,'keep exact native shell/cwd semantics');
    assert.equal(servers.identity.headers['X-Account'],'${ACCOUNT}','do not expand credentials in the bridge');
    assert.deepEqual(Object.keys(mcpDefinitions([{...selection,mode:'disabled'}])),[],'excluded helper cannot execute');
    assert.equal(existsSync(marker),false,'definition loading must not execute authentication');
  }
  for (const invalid of ['', '  ', null, 42, 'auth\0command']) {
    writeFileSync(source,JSON.stringify({mcpServers:{identity:{type:'http',url:'https://example.invalid',headersHelper:invalid}}}));
    assert.throws(()=>mcpDefinitions([selection]),/headersHelper/);
  }
  writeFileSync(source,JSON.stringify({mcpServers:{identity:{command:'python3',headersHelper:command}}}));
  assert.throws(()=>mcpDefinitions([selection]),/unsupported stdio/);
  const inventory=await mcpInventory({mcpServerStatus:async()=>[{name:'identity',status:'failed',source:'dynamic',config:{headersHelper:command,headers:{Authorization:'fixture-secret'}},error:'fixture-secret'}]});
  assert.equal(JSON.stringify(inventory).includes('fixture-secret'),false);
  assert.equal(JSON.stringify(inventory).includes('auth-tool'),false);
  console.log('PASS helper passthrough, no bridge execution/expansion, exclusion, invalid fields and redacted inventory');
} finally { rmSync(root,{recursive:true,force:true}); }
