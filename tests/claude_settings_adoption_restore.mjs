// Reopen the production Session from its committed native settings binding.
import { Session } from '../bridges/claude/dist/session.js';
import { fingerprint } from '../bridges/claude/dist/configuration.js';
import { readFileSync,writeFileSync } from 'node:fs';
const request=JSON.parse(readFileSync(process.argv[2],'utf8')),checks={};
const session=Session.restore(request.directory,request.id);let snapshot,error='';
try{
  await session.open('fixture-only');snapshot=await session.snapshot();
  checks.same_id=snapshot.config.id===request.id;
  checks.same_sources=fingerprint(snapshot.config.settings_sources)===fingerprint(request.sources);
  checks.guard_registered=snapshot.settings_guard?.state==='registered';
  if(request.recovery){
    checks.recovered_config=snapshot.configuration?.status==='recovered'&&snapshot.configuration.recovery?.status==='verified';
    checks.original_skills=fingerprint(snapshot.config.skill_selections)===fingerprint(request.skills);
    checks.original_plugins=fingerprint(snapshot.config.plugin_selections)===fingerprint(request.plugins);
    checks.actual_original_plugin=request.plugins.every(p=>snapshot.plugins?.packages.some(pack=>pack.name===p.name&&pack.version===p.version&&pack.path===p.source_path));
    checks.no_candidate_skill=snapshot.skills?.available&&!snapshot.skills.commands.some(c=>c.name.includes(request.retired_skill_name)||(c.aliases??[]).some(a=>a.includes(request.retired_skill_name)))&&!snapshot.skills.descriptions.some(d=>d.name.includes(request.retired_skill_name));
    checks.no_candidate_plugin_skill=snapshot.plugins?.skills.available&&!snapshot.plugins.skills.commands.some(c=>c.name.includes(request.retired_plugin_skill))&&!snapshot.plugins.skills.descriptions.some(d=>d.name.includes(request.retired_plugin_skill));
    checks.original_skill_description=request.skills.every(s=>snapshot.skills?.descriptions.some(d=>d.name===s.name||d.name===s.name.split(':').at(-1)&&d.plugin_name===s.plugin_name));
  }
  checks.no_input=snapshot.entries.every(e=>e.kind!=='input'&&e.event?.type!=='result');
}catch(e){error=e.message;checks.open=false;}
finally{
  await session.close();checks.closed=true;
  writeFileSync(request.output,JSON.stringify({checks,snapshot,error,passed:Object.values(checks).every(Boolean),scope:'production Session cold reopen with saved target sources; no model input'},null,2));
}
if(!Object.values(checks).every(Boolean))process.exitCode=1;
