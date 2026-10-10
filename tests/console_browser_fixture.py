#!/usr/bin/env python3
"""Owned interactive browser fixture with explicit local fault controls."""
import argparse
import json
import os
from pathlib import Path
import signal
import time

from codex_interaction_smoke import InteractionProvider
from console_fixture import ConsoleFixture


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--netcap", required=True)
    parser.add_argument("--info-file", required=True)
    args = parser.parse_args()
    fixture = ConsoleFixture(args.netcap, InteractionProvider)
    faults = fixture.root / "faults"
    faults.mkdir()
    fixture.env["NETCAP_FAULT_DIR"] = str(faults)
    fixture.config["instances"][0]["env_allow"].append("NETCAP_FAULT_DIR")
    fixture.config["instances"][0]["executable"] = str(Path(__file__).with_name("fault_codex_proxy.py").resolve())
    fixture.provider.stream_delay = 1.2
    finished = False

    def finish(*_):
        nonlocal finished
        finished = True

    signal.signal(signal.SIGINT, finish)
    signal.signal(signal.SIGTERM, finish)
    with fixture:
        # Reuse the selected port after crashes so the browser reconnects to
        # the same origin, just like a configured installation.
        fixture.config["listen"] = fixture.info["url"].removeprefix("http://")
        info = {**fixture.info, "root": str(fixture.root), "harness_pid": os.getpid(),
                "console_pid": fixture.console.pid, "native_pid": fixture.native.pid}
        Path(args.info_file).write_text(json.dumps(info, indent=2))
        print("READY", fixture.root, fixture.info["url"], flush=True)
        control = fixture.root / "control.json"
        while not finished:
            if control.exists():
                action = json.loads(control.read_text())
                control.unlink()
                kind = action["kind"]
                if kind == "crash_console":
                    fixture.restart_console(crash=True)
                elif kind == "delay":
                    fixture.provider.stream_delay = float(action["seconds"])
                elif kind == "stop":
                    finished = True
                else:
                    raise ValueError("unknown browser fixture action: " + kind)
                (fixture.root / "control-result.json").write_text(json.dumps({"kind": kind, "ok": True}))
            assert fixture.console.poll() is None and fixture.native.poll() is None
            time.sleep(.1)
        (fixture.root / "snapshot.json").write_text(json.dumps(fixture.snapshot(), indent=2))


if __name__ == "__main__":
    main()
