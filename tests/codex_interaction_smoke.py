#!/usr/bin/env python3
"""Real Codex permission callbacks through the console; no real provider."""
import argparse
import json
from pathlib import Path
import shlex
import time

from console_fixture import ConsoleFixture, Provider, wait_for


class InteractionProvider(Provider):
    def __init__(self,root,route):
        super().__init__(root,route)
        self.workspace=root/"work"
        self.stages={}

    def responses(self,payload,answer,seq):
        body=json.dumps(payload.get("input",[]))
        kind=next((key for key in ("APPROVE","DECLINE","EXPIRE","QUESTION","BACKGROUND") if "NETCAP_"+key in body),None)
        key=payload.get("client_metadata",{}).get("turn_id",payload.get("prompt_cache_key"))
        stage=self.stages.get(key,0);self.stages[key]=stage+1
        if not kind or stage>0:return super().responses(payload,answer,seq)
        if kind=="QUESTION":
            code='text(await tools.request_user_input_async({questions:[{title:"Fixture color?",options:["green","blue"]}]}));'
        elif kind=="BACKGROUND":
            code='text(await tools.exec_command({cmd:"sleep 90",yield_time_ms:1000,login:false}));'
        else:
            command="printf 'approved\\n' > "+shlex.quote(str(self.workspace/(kind.lower()+".txt")))
            code="text(await tools.exec_command("+json.dumps({"cmd":command,"sandbox_permissions":"require_escalated","justification":"Allow the isolated fixture to write its own result file?","login":False})+"));"
        item={"id":f"ctc_{seq}","type":"custom_tool_call","call_id":f"call_{seq}","name":"exec","namespace":"functions","input":code,"status":"completed"}
        if kind=="QUESTION":
            item={"id":f"fc_{seq}","type":"function_call","call_id":f"call_{seq}","name":"request_user_input_async","namespace":"functions",
                  "arguments":json.dumps({"questions":[{"title":"Fixture color?","options":["green","blue"]}]}),"status":"completed"}
        response={"id":f"resp_{seq}","object":"response","created_at":int(time.time()),"status":"completed","model":payload["model"],"output":[item],"usage":{"input_tokens":32,"output_tokens":16,"total_tokens":48}}
        return self.sse([("response.created",{"response":dict(response,status="in_progress",output=[])}),
                         ("response.output_item.added",{"output_index":0,"item":dict(item,status="in_progress")}),
                         ("response.output_item.done",{"output_index":0,"item":item}),
                         ("response.completed",{"response":response})])


def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument("--netcap",required=True);args=parser.parse_args()
    with ConsoleFixture(args.netcap,InteractionProvider) as fixture:
        print("Evidence directory:",fixture.root,flush=True)
        for kind,decision in (("APPROVE","accept"),("DECLINE","cancel")):
            sid=fixture.create(kind.lower())
            fixture.command(kind+"-send","send",session_id=sid,text="NETCAP_"+kind)
            def pending():return next((p for p in fixture.session(sid)["view"]["pending"] if p["status"]=="pending"),None)
            action=wait_for(pending,"real native approval callback")
            (fixture.root/(kind.lower()+"-request.json")).write_text(json.dumps(action,indent=2))
            option=next(o for o in action["options"] if o["value"]==decision)
            first=fixture.command(kind+"-reply","respond",session_id=sid,pending_id=action["id"],option_id=option["id"])
            assert fixture.command(kind+"-reply","respond",session_id=sid,pending_id=action["id"],option_id=option["id"])==first
            wait_for(lambda:fixture.session(sid)["status"]=="idle","approval turn terminates")
            exists=(fixture.cwd/(kind.lower()+".txt")).exists()
            assert exists==(decision=="accept"),(kind,exists)
            print("PASS native",kind,"and duplicate reply; file exists:",exists,flush=True)

        fixture.provider.stream_delay=1.5
        sid=fixture.create("question")
        fixture.command("question-send","send",session_id=sid,text="NETCAP_QUESTION")
        action=wait_for(lambda:next((p for p in fixture.session(sid)["view"]["pending"] if p["status"]=="pending"),None),"native question callback")
        (fixture.root/"question-request.json").write_text(json.dumps(action,indent=2))
        assert action["response_kind"] in ("user_input","async_input","message_input"),action
        answers={q["id"]:["green"] for q in action["questions"]}
        fixture.command("question-reply","respond",session_id=sid,pending_id=action["id"],answers=answers)
        wait_for(lambda:fixture.session(sid)["status"]=="idle","question completes")
        print("PASS native question reply:",action["response_kind"],flush=True)
        (fixture.root/"snapshot.json").write_text(json.dumps(fixture.snapshot(),indent=2))


if __name__=="__main__":main()
