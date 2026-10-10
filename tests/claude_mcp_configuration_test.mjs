// Verification must distinguish restoring native inheritance from leaving an
// old injected definition active, even when both expose the same tool name.
import assert from 'node:assert/strict';
import { mcpMatches } from '../bridges/claude/dist/mcp.js';
const old = [{ asset_id:'asset', server:'identity', mode:'enabled', source_path:'/fixture/old.json' }];
const inventory = source => ({ available:true, at:'fixture', servers:[{ name:'identity', status:'connected', source, tools:['whoami'] }] });
assert.equal(mcpMatches([], inventory('dynamic'), old), false, 'released override still present');
assert.equal(mcpMatches([], inventory(''), old), false, 'unknown origin cannot prove inheritance');
assert.equal(mcpMatches([], inventory('project'), old), true, 'native project inheritance preserved');
assert.equal(mcpMatches([], {available:true,at:'fixture',servers:[]}, old), true, 'no inherited counterpart is valid');
assert.equal(mcpMatches([], {available:false,at:'fixture',servers:[]}, old), false, 'unreadable inventory is not proof');
assert.equal(mcpMatches(old, inventory('project')), false, 'inherited counterpart cannot stand in for explicit target');
assert.equal(mcpMatches(old, inventory('dynamic')), true, 'explicit target uses dynamic source');
console.log('PASS 7 MCP configuration source-proof checks');
