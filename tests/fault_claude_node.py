#!/usr/bin/python3
"""Test-only Node launcher that drops one SDK bridge receipt, not execution."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import threading

root = Path(os.environ["NETCAP_CLAUDE_FAULT_DIR"]).resolve()
assert str(root).startswith("/tmp/netcap-console-")
root.mkdir(exist_ok=True)
child = subprocess.Popen([shutil.which("node"), *sys.argv[1:]], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                         stderr=subprocess.DEVNULL, text=True, pass_fds=(3,))
methods = {}


def send_input():
    try:
        for line in sys.stdin:
            message = json.loads(line)
            methods[message["id"]] = message["method"]
            # Credentials are inside params; log only the method name.
            with (root / "calls.jsonl").open("a") as log:
                log.write(json.dumps({"method": message["method"]}) + "\n")
            child.stdin.write(line)
            child.stdin.flush()
    finally:
        child.stdin.close()


threading.Thread(target=send_input, daemon=True).start()
try:
    for line in child.stdout:
        message = json.loads(line)
        armed = root / "armed.json"
        try:
            fault = json.loads(armed.read_text())
        except FileNotFoundError:
            fault = {}
        if "result" in message and methods.get(message["id"]) == fault.get("method"):
            armed.unlink()
            (root / "dropped.json").write_text(json.dumps({"method": fault["method"], "result": message["result"]}))
            continue
        sys.stdout.write(line)
        sys.stdout.flush()
finally:
    child.terminate()
    try:
        child.wait(timeout=5)
    except subprocess.TimeoutExpired:
        child.kill()
        child.wait()
