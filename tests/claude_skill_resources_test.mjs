import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, symlinkSync, linkSync, copyFileSync, writeFileSync, rmSync, unlinkSync, existsSync, readlinkSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { prepareSkillResources } from '../bridges/claude/dist/skill_resources.js';
import { skillSelections, verifySkillSources, skillsMatch } from '../bridges/claude/dist/skills.js';

function fixture(t, kind = 'directory', mode = 'enabled') {
  const root = mkdtempSync(join(tmpdir(), 'netcap-skill-resource-'));
  const original = process.env.CLAUDE_CONFIG_DIR;
  process.env.CLAUDE_CONFIG_DIR = join(root, 'profile');
  t.after(() => { if (original === undefined) delete process.env.CLAUDE_CONFIG_DIR; else process.env.CLAUDE_CONFIG_DIR = original; rmSync(root, { recursive: true, force: true }); });
  for (const dir of ['source', 'state', 'work', 'profile']) mkdirSync(join(root, dir));
  writeFileSync(join(root, 'source/SKILL.md'), '---\nname: display\ndescription: fixture\n---\nprivate method');
  writeFileSync(join(root, 'source/sibling.txt'), 'not a single-file dependency');
  const plugin = 'netcap-skill-' + 'a'.repeat(24), hash = 'b'.repeat(64), state = join(root, 'state');
  const packageRoot = join(state, 'skill-resources', plugin, hash);
  const selection = skillSelections([{asset_id:'asset', name:plugin+':display', native_key:plugin+':method', mode, source:'plugin', source_path:join(root,'source/SKILL.md'), discovery_path:join(packageRoot,'skills/method/SKILL.md'), delivery:'local_plugin', plugin_name:plugin, resource_root:join(root, kind==='directory'?'source':'source/SKILL.md'), resource_kind:kind, resource_hash:hash}])[0];
  const config = {cwd:join(root,'work'),skill_selections:[selection]};
  const inventory = {available:true,at:'now',commands:[{name:selection.name,builtin:false,aliases:['display',selection.native_key]}],descriptions:[{name:selection.name,source:'plugin',plugin_name:plugin}]};
  return {root,state,packageRoot,selection,config,inventory};
}

for (const kind of ['directory','file']) test(`${kind} delivery is repeatable and links only its registered resource`,t=>{
  const f=fixture(t,kind),plugins=prepareSkillResources(f.state,f.config);
  assert.deepEqual(plugins,[{type:'local',path:f.packageRoot}]);
  assert.deepEqual(prepareSkillResources(f.state,f.config),plugins);verifySkillSources(f.config);
  const entry=join(f.packageRoot,'skills/method');
  assert.equal(readlinkSync(kind==='directory'?entry:join(entry,'SKILL.md')),f.selection.resource_root);
  assert.equal(existsSync(join(entry,'sibling.txt')),kind==='directory');
});
test('disabled delivery creates no package and passes no plugin',t=>{
  const f=fixture(t,'directory','disabled');assert.deepEqual(prepareSkillResources(f.state,f.config),[]);assert.equal(existsSync(f.packageRoot),false);verifySkillSources(f.config);
});
for (const mutation of ['root-link','parent-link','extra-hook','manifest','changed-link','partial','foreign-root']) test(`${mutation} fails without overwriting package contents`,t=>{
  const f=fixture(t);prepareSkillResources(f.state,f.config);
  if(mutation==='root-link'){rmSync(f.packageRoot,{recursive:true});symlinkSync(join(f.root,'source'),f.packageRoot);}
  if(mutation==='parent-link'){rmSync(join(f.state,'skill-resources'),{recursive:true});symlinkSync(join(f.root,'source'),join(f.state,'skill-resources'));}
  if(mutation==='extra-hook')mkdirSync(join(f.packageRoot,'hooks'));
  if(mutation==='manifest')writeFileSync(join(f.packageRoot,'.claude-plugin/plugin.json'),'different');
  if(mutation==='changed-link'){unlinkSync(join(f.packageRoot,'skills/method'));symlinkSync(join(f.root,'work'),join(f.packageRoot,'skills/method'));}
  if(mutation==='partial')unlinkSync(join(f.packageRoot,'.claude-plugin/plugin.json'));
  assert.throws(()=>prepareSkillResources(mutation==='foreign-root'?join(f.root,'work'):f.state,f.config));
});
test('new natural entry blocks independent delivery even when disabled',t=>{
  const f=fixture(t,'directory','disabled');mkdirSync(join(f.root,'profile/skills'));symlinkSync(join(f.root,'source'),join(f.root,'profile/skills/added'));
  assert.throws(()=>verifySkillSources(f.config),/新增了原生入口/);
});
for(const mode of ['enabled','disabled']) test(`native hard-link entry blocks external ${mode} selection`,t=>{
  const f=fixture(t,'directory',mode),other=join(f.root,'profile/skills/linked');
  prepareSkillResources(f.state,f.config);
  mkdirSync(other,{recursive:true});linkSync(f.selection.source_path,join(other,'SKILL.md'));
  assert.throws(()=>verifySkillSources(f.config),/原生入口|别名/);
});
for(const mode of ['enabled','disabled']) test(`an independent native copy is not an external ${mode} file alias`,t=>{
  const f=fixture(t,'directory',mode),other=join(f.root,'profile/skills/copied-key');
  prepareSkillResources(f.state,f.config);
  mkdirSync(other,{recursive:true});copyFileSync(f.selection.source_path,join(other,'SKILL.md'));
  assert.doesNotThrow(()=>verifySkillSources(f.config));
});
for(const mutation of ['none','alias','builtin-alias','wrong-plugin','extra-component','disabled-other-component','disabled-namespace-alias']) test(`inventory ${mutation} validates namespace, origin and alias uniqueness`,t=>{
  const f=fixture(t);
  if(mutation.includes('alias'))f.inventory.commands.push({name:'other',aliases:['display'],builtin:mutation==='builtin-alias'});
  if(mutation==='wrong-plugin')f.inventory.descriptions[0].plugin_name='other';
  if(mutation==='extra-component')f.inventory.commands.push({name:f.selection.plugin_name+':extra',aliases:[],builtin:false});
  if(mutation==='disabled-namespace-alias'){f.selection.mode='disabled';f.inventory.commands=[{name:'other',aliases:[f.selection.native_key],builtin:false}];f.inventory.descriptions=[];}
  if(mutation==='disabled-other-component'){f.selection.mode='disabled';f.inventory.commands[0].name=f.selection.plugin_name+':extra';f.inventory.descriptions=[];}
  assert.equal(skillsMatch([f.selection],f.inventory),mutation==='none');
});
