import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, symlinkSync, linkSync, copyFileSync, writeFileSync, rmSync, unlinkSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { applySkillSelections, skillSelections, skillInventory, verifySkillSources, verifySkills } from '../bridges/claude/dist/skills.js';

function fixture(t) {
  const root=mkdtempSync(join(tmpdir(),'netcap-skill-contract-'));
  const original=process.env.CLAUDE_CONFIG_DIR;
  process.env.CLAUDE_CONFIG_DIR=join(root,'profile');
  t.after(()=>{if(original===undefined)delete process.env.CLAUDE_CONFIG_DIR;else process.env.CLAUDE_CONFIG_DIR=original;rmSync(root,{recursive:true,force:true});});
  const source=join(root,'source'),entry=join(root,'profile/skills/native-key');
  mkdirSync(source,{recursive:true});mkdirSync(join(root,'profile/skills'),{recursive:true});mkdirSync(join(root,'work'));
  writeFileSync(join(source,'SKILL.md'),'---\nname: display\ndescription: fixture\n---\nprivate method');symlinkSync(source,entry);
  const selections=skillSelections([{asset_id:'asset',name:'display',native_key:'native-key',mode:'disabled',source:'userSettings',source_path:join(source,'SKILL.md'),discovery_path:join(entry,'SKILL.md')}]);
  const config={cwd:join(root,'work'),skill_selections:selections};
  let visible=true,dropOther=false;
  const run={ calls:[],
    supportedCommands:async()=>[...(visible?[{name:'display'}]:[]),...(!dropOther?[{name:'other'}]:[])],
    getContextUsage:async options=>{assert.deepEqual(options,{detail:'summary'});return {skills:{skillFrontmatter:[...(visible?[{name:'display',source:'userSettings'}]:[]),...(!dropOther?[{name:'other',source:'projectSettings'}]:[])]}};},
    applyFlagSettings:async flags=>{run.calls.push(flags);assert.equal(flags.skillOverrides['native-key'],'off');visible=false;},
    reloadSkills:async()=>{},
  };
  return {root,source,entry,config,run,show:()=>{visible=true;},drop:()=>{dropOther=true;}};
}

test('selection uses native key, preserves prior flag overrides, and verifies source',async t=>{
  const f=fixture(t);await applySkillSelections(f.run,f.config,{skillOverrides:{unrelated:'off'}});
  assert.deepEqual(f.run.calls,[{skillOverrides:{unrelated:'off','native-key':'off'}}]);
  await verifySkills(f.run,f.config);
  f.show();await assert.rejects(verifySkills(f.run,f.config),/不一致/);
});
test('an inherited command removed by the override rejects the query',async t=>{
  const f=fixture(t),apply=f.run.applyFlagSettings;
  f.run.applyFlagSettings=async flags=>{await apply(flags);f.drop();};
  await assert.rejects(applySkillSelections(f.run,f.config,{}),/其他继承/);
});
test('same-key project source blocks before any native flag write',async t=>{
  const f=fixture(t),project=join(f.config.cwd,'.claude/skills/native-key');mkdirSync(project,{recursive:true});writeFileSync(join(project,'SKILL.md'),'other');
  await assert.rejects(applySkillSelections(f.run,f.config,{}),/同目录键/);assert.equal(f.run.calls.length,0);
});
test('changed shared symlink invalidates inventory and input',async t=>{
  const f=fixture(t);unlinkSync(f.entry);mkdirSync(f.entry);writeFileSync(join(f.entry,'SKILL.md'),'replacement');
  assert.throws(()=>verifySkillSources(f.config),/入口变化/);
  assert.equal((await skillInventory(f.run,f.config)).available,false);
});
for (const alias of ['symlink','hardlink']) test(`new ${alias} under another key invalidates natural selection`,async t=>{
  const f=fixture(t),other=join(f.config.cwd,'.claude/skills/other-key');
  mkdirSync(join(f.config.cwd,'.claude/skills'),{recursive:true});
  if(alias==='symlink')symlinkSync(f.source,other);
  else{mkdirSync(other);linkSync(join(f.source,'SKILL.md'),join(other,'SKILL.md'));}
  assert.throws(()=>verifySkillSources(f.config),/入口|别名/);
  assert.equal((await skillInventory(f.run,f.config)).available,false);
  await assert.rejects(verifySkills(f.run,f.config));
  assert.equal(f.run.calls.length,0);
});
test('ambiguous native display name blocks before configuration',async t=>{
  const f=fixture(t),list=f.run.supportedCommands;f.run.supportedCommands=async()=>[...await list(),{name:'display'}];
  await assert.rejects(applySkillSelections(f.run,f.config,{}),/不唯一/);assert.equal(f.run.calls.length,0);
});
test('an independent copy is not a file alias; native name conflicts remain checked',async t=>{
  const f=fixture(t),other=join(f.config.cwd,'.claude/skills/copied-key');
  mkdirSync(other,{recursive:true});copyFileSync(join(f.source,'SKILL.md'),join(other,'SKILL.md'));
  assert.doesNotThrow(()=>verifySkillSources(f.config));
  const list=f.run.supportedCommands;f.run.supportedCommands=async()=>[...await list(),{name:'display'}];
  await assert.rejects(applySkillSelections(f.run,f.config,{}),/不唯一/);
  assert.equal(f.run.calls.length,0);
});
test('invalid original visibility is rejected without quoting its value',async t=>{
  const f=fixture(t);
  await assert.rejects(applySkillSelections(f.run,f.config,{skillOverrides:{other:'private-value'}}),e=>/设置无效/.test(e.message)&&!e.message.includes('private-value'));
  assert.equal(f.run.calls.length,0);
});
