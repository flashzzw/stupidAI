#!/usr/bin/python3
"""Test-only transparent CLI proxy; faults apply only to the explicit temp dir.

This wraps the real CLI, never an alternative native server or fake runtime.
It can drop one selected RPC response or mark a history page incomplete.
"""
import json
import os
from pathlib import Path
import struct
import subprocess
import sys
import threading

REAL="/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex"
if sys.argv[1:3]!=["app-server","proxy"]:
    os.execv(REAL,[REAL,*sys.argv[1:]])

root=Path(os.environ["NETCAP_FAULT_DIR"]).resolve()
assert str(root).startswith("/tmp/netcap-console-")
root.mkdir(exist_ok=True)
child=subprocess.Popen([REAL,*sys.argv[1:]],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL)
calls={}
discovery_calls=set()
lock=threading.Lock()


def exact(stream,size):
    out=b""
    while len(out)<size:
        part=stream.read(size-len(out))
        if not part:raise EOFError
        out+=part
    return out


def handshake(source,target):
    header=b""
    while not header.endswith(b"\r\n\r\n"):
        header+=exact(source,1)
        assert len(header)<16384
    target.write(header);target.flush()


def frame(stream):
    header=exact(stream,2);size=header[1]&127
    if size==126:
        ext=exact(stream,2);header+=ext;size=struct.unpack("!H",ext)[0]
    elif size==127:
        ext=exact(stream,8);header+=ext;size=struct.unpack("!Q",ext)[0]
    mask=exact(stream,4) if header[1]&128 else None
    body=exact(stream,size)
    payload=bytes(b^mask[n%4] for n,b in enumerate(body)) if mask else body
    message=None
    if header[0]&0x8f==0x81:
        try:message=json.loads(payload)
        except (ValueError,UnicodeDecodeError):pass
    return header+(mask or b"")+body,message


def encode(message):
    body=json.dumps(message).encode()
    if len(body)<126:header=bytes([0x81,len(body)])
    elif len(body)<65536:header=b"\x81\x7e"+struct.pack("!H",len(body))
    else:header=b"\x81\x7f"+struct.pack("!Q",len(body))
    return header+body


def input_loop():
    try:
        handshake(sys.stdin.buffer,child.stdin)
        while True:
            wire,message=frame(sys.stdin.buffer)
            if message and "id" in message and "method" in message:
                with lock:
                    calls[str(message["id"])]=message["method"]
                    if message["method"]=="thread/loaded/list" or (message["method"]=="thread/list" and "ancestorThreadId" not in message.get("params",{})):
                        discovery_calls.add(str(message["id"]))
                with (root/"calls.jsonl").open("a") as log:
                    log.write(json.dumps({"method":message["method"],"params":message.get("params",{})})+"\n")
            child.stdin.write(wire);child.stdin.flush()
    except (EOFError,BrokenPipeError,OSError):
        try:child.stdin.close()
        except OSError:pass


threading.Thread(target=input_loop,daemon=True).start()
try:
    handshake(child.stdout,sys.stdout.buffer)
    while True:
        wire,message=frame(child.stdout)
        with lock:method=calls.get(str(message.get("id"))) if message else None
        armed=root/"armed.json"
        try:fault=json.loads(armed.read_text())
        except (FileNotFoundError,ValueError):fault={}
        if message and fault.get("mode")=="hide_discovery" and str(message.get("id")) in discovery_calls and "result" in message:
            wire=encode({"id":message["id"],"error":{"code":-32000,"message":"fixture: native discovery unavailable"}})
        if message and method==fault.get("method") and "result" in message:
            if fault.get("mode")=="drop":
                (root/"dropped.json").write_text(json.dumps({"method":method,"result":message["result"]},indent=2))
                if fault.get("next"):armed.write_text(json.dumps(fault["next"]))
                else:armed.unlink()
                break
            if fault.get("mode")=="partial":
                result=message["result"]
                if "thread" in result:
                    for turn in result["thread"].get("turns",[]):turn["itemsView"]="partial"
                else:result["nextCursor"]="fixture-unfinished-page"
                wire=encode(message)
        sys.stdout.buffer.write(wire);sys.stdout.buffer.flush()
except (EOFError,BrokenPipeError,OSError):
    pass
finally:
    child.terminate()
    try:child.wait(timeout=3)
    except subprocess.TimeoutExpired:child.kill();child.wait()
