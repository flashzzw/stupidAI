import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { test } from 'node:test';
import { configurationNoticeHooks } from '../bridges/claude/dist/configuration_notices.js';

const context = {native_id: randomUUID(), cwd: '/owned', query_id: randomUUID(), configuration_id: 'b'.repeat(64)};
const input = {hook_event_name: 'ConfigChange', session_id: context.native_id, cwd: '/owned/nested',
  source: 'policy_settings', file_path: '/etc/claude-code/managed-settings.json', prompt_id: randomUUID()};
const invoke = (value, append, failed) => configurationNoticeHooks(context, append, failed).ConfigChange[0].hooks[0](value, undefined, {signal: new AbortController().signal});

test('native configuration metadata retains independent identities and unknown sources without file contents', async () => {
  const saved = [];
  assert.deepEqual(await invoke({...input, source: 'future_中文\n\u202E', agent_id: 'worker', agent_type: 'reviewer',
    content: 'PRIVATE_BODY', transcript_path: '/PRIVATE_TRANSCRIPT', operation: 'UNTRUSTED_INPUT', version_hash: 'UNTRUSTED_VERSION'}, n => saved.push(n), () => assert.fail('unexpected failure')), {});
  const notice = saved[0];
  assert.equal(notice.query_id, context.query_id); assert.equal(notice.configuration_id, context.configuration_id);
  assert.equal(notice.cwd, '/owned'); assert.equal(notice.native_cwd, '/owned/nested');
  assert.equal(notice.prompt_id, input.prompt_id); assert.equal(notice.source, 'future_中文\n\u202E');
  assert.equal(notice.agent_id, 'worker'); assert.equal(notice.path, input.file_path);
  assert.ok(notice.id && notice.at); assert.doesNotMatch(JSON.stringify(notice), /PRIVATE|UNTRUSTED/);
});

test('a source notification without a file or native prompt remains a valid observation', async () => {
  let notice;
  await invoke({...input, source: 'skills', file_path: undefined, prompt_id: undefined}, n => {notice = n;}, () => assert.fail('optional metadata rejected'));
  assert.equal(notice.path, ''); assert.equal(notice.prompt_id, undefined); assert.equal(notice.source, 'skills');
});

for (const [name, value] of Object.entries({
  'foreign session': {session_id: randomUUID()}, 'wrong event': {hook_event_name: 'PreToolUse'},
  'relative cwd': {cwd: 'owned'}, 'relative path': {file_path: 'settings.json'}, 'NUL path': {file_path: '/owned/\0'},
  'oversized UTF-8 path': {file_path: '/' + '中'.repeat(1400)}, 'missing source': {source: ''},
  'invalid prompt': {prompt_id: 'not-a-native-uuid'}, 'invalid agent': {agent_id: null},
})) test(name + ' produces a recording gap without making a native decision', async () => {
  let failed = 0, saved = 0;
  assert.deepEqual(await invoke({...input, ...value}, () => {saved++;}, () => {failed++;}), {});
  assert.equal(saved, 0); assert.equal(failed, 1);
});

test('both recording and diagnostic failures leave native config decisions unchanged', async () => {
  assert.deepEqual(await invoke(input, () => {throw Error('disk full');}, () => {throw Error('secondary error');}), {});
});
