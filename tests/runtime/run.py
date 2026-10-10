#!/usr/bin/env python3
"""Real CLI + local provider contracts. Never reads the user's runtime profiles."""
import argparse
import importlib
import json
from pathlib import Path
import shutil
import subprocess
import tempfile

from common import Provider

PINNED = {"codex": "0.157.1", "codex_server": "0.158.0", "claude": "2.1.280", "opencode": "1.18.32"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--agent", choices=("all", "codex", "claude", "opencode"), default="all")
    parser.add_argument("--codex", default=shutil.which("codex"))
    parser.add_argument("--codex-server", default=str(Path.home() / ".codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex"))
    parser.add_argument("--claude", default=shutil.which("claude"))
    parser.add_argument("--opencode", default=shutil.which("opencode"))
    parser.add_argument("--node", default=shutil.which("node"))
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix="netcap-runtime-"))
    print(f"Evidence directory: {root}", flush=True)
    agents = ["codex", "claude", "opencode"] if args.agent == "all" else [args.agent]
    results = {"versions": {}, "agents": {}}
    try:
        for agent in agents:
            keys = ("codex", "codex_server") if agent == "codex" else (agent,)
            for key in keys:
                binary = getattr(args, key)
                version = subprocess.check_output([binary, "--version"], text=True, timeout=10).strip()
                assert PINNED[key] in version, f"{key}: expected {PINNED[key]}, got {version}; revalidate before changing pin"
                results["versions"][key] = {"path": str(Path(binary).resolve()), "version": version}
            module = importlib.import_module(agent + "_probe")
            agent_root = root / agent
            agent_root.mkdir()
            results["agents"][agent] = {}
            for route in ("A", "B"):
                case_root = agent_root / route
                case_root.mkdir()
                provider = Provider(case_root, route)
                try:
                    result = module.probe(case_root, provider, args)
                    results["agents"][agent][route] = result
                    print(f"PASS {agent}/{route}: two turns, completed-session recovery, third turn, route+auth+history", flush=True)
                finally:
                    provider.close()
            assert results["agents"][agent]["A"]["native_id"] != results["agents"][agent]["B"]["native_id"]
    finally:
        (root / "results.json").write_text(json.dumps(results, indent=2))


if __name__ == "__main__":
    main()
