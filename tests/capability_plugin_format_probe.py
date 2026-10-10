#!/usr/bin/env python3
"""Read native plugin discovery from an owned Codex daemon; no model turns."""
import json
from pathlib import Path

from console_fixture import ConsoleFixture,CLI
from go_cli_smoke import Proxy
from execution_config_probe import write,skill


def main():
    f=ConsoleFixture('');client=None
    try:
        config=f.profile/'config.toml';config.write_text(config.read_text().replace('plugins = false','plugins = true'))
        cases={'root-only':{'name':'root-only'},'inline':{'name':'inline','extensions':{'com.openai':{}}},'fallback':{'name':'fallback'}}
        plugins=[]
        for name,manifest in cases.items():
            root=f.cwd/'plugins'/name
            write(root/'plugin.json',manifest)
            skill(root/'skills/method',name+'-method','NATIVE_FORMAT_'+name)
            if name!='root-only':write(root/'.codex-plugin/plugin.json',{'name':name,'version':'1.0.0'})
            plugins.append({'name':name,'source':{'source':'local','path':'./plugins/'+name},'policy':{'installation':'AVAILABLE','authentication':'ON_INSTALL'},'category':'Productivity'})
        market=f.cwd/'.agents/plugins/marketplace.json';write(market,{'name':'format-fixture','plugins':plugins})
        f.start_native();client=Proxy(CLI,f.sock,f.env)
        observed={}
        for name in cases:
            try:observed[name]={'install':client.call('plugin/install',{'pluginName':name,'marketplacePath':str(market)})}
            except AssertionError as e:observed[name]={'error':str(e)}
        listing=client.call('skills/list',{'cwds':[str(f.cwd)],'forceReload':True})
        for name,value in observed.items():value['skill_discovered']=('NATIVE_FORMAT_'+name) in json.dumps(listing)
        assert not f.provider.requests
        result={'scope':'owned Codex 0.157.1/0.158.0; plugin installation and skill discovery only; no model calls or production selection claim','observations':observed,'skills':listing,'no_model_calls':True}
        write(f.root/'format-probe.json',result)
        print('Evidence directory:',f.root,flush=True);print(json.dumps(result),flush=True)
    finally:
        if client:client.close()
        f.close()

if __name__=='__main__':main()
