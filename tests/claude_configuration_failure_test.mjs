import test from 'node:test';
import assert from 'node:assert/strict';
import { configurationFailure } from '../bridges/claude/dist/configuration.js';
const valid={version:1,operation:'owned-operation',baseline:'a'.repeat(64),stage:'check_settings',reason:'settings_parse_errors',cleanup:'confirmed',at:'2026-10-06T00:00:00.000Z'};
test('failure diagnostics copy only the fixed schema',()=>{
 assert.deepEqual(configurationFailure({...valid,error:'PRIVATE_ERROR',settings:{token:'PRIVATE_KEY'},detail:'PRIVATE_DETAIL'}),valid);
});
test('unknown or inconsistent persisted diagnostics cannot become current evidence',()=>{
 for(const extra of [{version:2},{operation:'x\nprivate'},{baseline:'wrong'},{stage:'unsupported'},{reason:'arbitrary error'},{stage:'commit',reason:'settings_parse_errors'},{cleanup:'probably'},{at:'bad'}])assert.equal(configurationFailure({...valid,...extra}),undefined);
});
