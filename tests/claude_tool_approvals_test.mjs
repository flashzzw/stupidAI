import test from 'node:test';
import assert from 'node:assert/strict';
import { requiredToolApprovalHooks } from '../bridges/claude/dist/tool_approvals.js';

const matcher = config => new RegExp(requiredToolApprovalHooks(config).PreToolUse[0].matcher);
test('read-only review covers writes and commands without changing read tools', () => {
  const match = matcher({permission:'read_only'});
  for (const name of ['Bash','Write','Edit','NotebookEdit']) assert.equal(match.test(name),true,name);
  for (const name of ['Read','Glob','Grep','Task','WriteOther','mcp__inherited__call']) assert.equal(match.test(name),false,name);
});
test('workspace sessions do not acquire unrelated tool restrictions', () => {
  assert.deepEqual(requiredToolApprovalHooks({permission:'workspace'}),{});
});
test('selected MCP and plugin namespaces retain per-call review in either permission mode', () => {
  const config={permission:'workspace',mcp_selections:[{server:'one.server',mode:'enabled'},{server:'disabled',mode:'disabled'}],
    plugin_selections:[{mode:'enabled',components:[{kind:'mcp',native_name:'plugin:selected:tools'}]}]};
  const match=matcher(config);
  for(const name of ['mcp__one.server__call','mcp__plugin_selected_tools__run'])assert.equal(match.test(name),true,name);
  for(const name of ['mcp__oneXserver__call','mcp__disabled__call','mcp__other__run','mcp__plugin_selected_other__run','Bash'])assert.equal(match.test(name),false,name);
});
test('review hook only asks and leaves tool arguments to native policy and callback', async () => {
  const callback=requiredToolApprovalHooks({permission:'read_only'}).PreToolUse[0].hooks[0];
  const input={hook_event_name:'PreToolUse',session_id:'owned',tool_name:'Edit',tool_input:{file_path:'/owned/file',new_string:'value'}};
  const result=await callback(input,'use',{signal:new AbortController().signal});
  assert.equal(result.hookSpecificOutput.hookEventName,'PreToolUse');
  assert.equal(result.hookSpecificOutput.permissionDecision,'ask');
  assert.equal(result.hookSpecificOutput.updatedInput,undefined);
  assert.equal(result.decision,undefined);
});
