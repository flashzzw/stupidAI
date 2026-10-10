import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync,writeFileSync,realpathSync,statSync,existsSync,symlinkSync,unlinkSync,chmodSync,mkdirSync,rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { settingsSources,verifySettingsSources } from '../bridges/claude/dist/settings_sources.js';
const fixture=t=>{const p=mkdtempSync(join(tmpdir(),'netcap-settings-source-test-'));t.after(()=>rmSync(p,{recursive:true,force:true}));return p;};
const stamp=path=>({path,resolved_path:realpathSync(path),kind:'file',sha256:createHash('sha256').update('v1').digest('hex'),mode:statSync(path).mode&0o777});
test('bound bytes and permissions are checked independently of JSON content',t=>{
 const root=fixture(t),path=join(root,'settings.json');writeFileSync(path,'v1',{mode:0o600});const source=stamp(path);
 assert.doesNotThrow(()=>verifySettingsSources([source]));writeFileSync(path,'PRIVATE_v2');assert.throws(()=>verifySettingsSources([source]),e=>e.message.includes(path)&&!e.message.includes('PRIVATE_v2'));
 writeFileSync(path,'v1');chmodSync(path,0o640);assert.throws(()=>verifySettingsSources([source]));
});
test('same bytes at a retargeted link are a different binding',t=>{
 const root=fixture(t),a=join(root,'a'),b=join(root,'b'),link=join(root,'settings.json');writeFileSync(a,'v1');writeFileSync(b,'v1');symlinkSync(a,link);const source=stamp(link);
 unlinkSync(link);symlinkSync(b,link);assert.throws(()=>verifySettingsSources([source]));
});
test('absence does not create directories and cannot hide a broken parent link',t=>{
 const root=fixture(t),path=join(root,'missing/settings.json');const source={path,resolved_path:path,kind:'absent'};
 assert.doesNotThrow(()=>verifySettingsSources([source]));assert.equal(existsSync(join(root,'missing')),false);
 symlinkSync(join(root,'unavailable'),join(root,'missing'));assert.throws(()=>verifySettingsSources([source]));
});
test('file type and size cannot replace a bound regular file',t=>{
 const root=fixture(t),path=join(root,'settings.json');writeFileSync(path,'v1');const source=stamp(path);unlinkSync(path);mkdirSync(path);assert.throws(()=>verifySettingsSources([source]));rmSync(path,{recursive:true});writeFileSync(path,Buffer.alloc(2*1024*1024+1));assert.throws(()=>verifySettingsSources([source]));
});
test('wire binding rejects duplicate paths and malformed versions; legacy performs no reads',()=>{
 const absent={path:'/owned/settings.json',resolved_path:'/owned/settings.json',kind:'absent'};
 assert.throws(()=>settingsSources([absent,absent]));
 assert.throws(()=>settingsSources([absent,{...absent,path:'/other',kind:'file',sha256:'invalid',mode:0o600}]));
 assert.doesNotThrow(()=>verifySettingsSources(undefined));
});
