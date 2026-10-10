import json
from pathlib import Path
import subprocess

from common import isolated_env, stop


def probe(root, provider, args):
    env = isolated_env(root)
    env.update(ANTHROPIC_BASE_URL=provider.url, ANTHROPIC_API_KEY="fixture-" + provider.route,
               CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1", DISABLE_AUTOUPDATER="1",
               DISABLE_TELEMETRY="1", DISABLE_ERROR_REPORTING="1")
    cwd = root / "work"
    cwd.mkdir()
    config = {"root": str(root), "cwd": str(cwd), "route": provider.route, "executable": args.claude}
    config_file = root / "probe.json"
    config_file.write_text(json.dumps(config))
    script = Path(__file__).with_suffix(".mjs")
    # Separate Node and CLI processes prove recovery is disk-backed.
    for mode in ("start", "resume"):
        with (root / f"{mode}.log").open("w") as log:
            process = subprocess.Popen([args.node, str(script), str(config_file), mode], env=env,
                                       cwd=cwd, stdout=log, stderr=log, start_new_session=True)
            try:
                assert process.wait(timeout=60) == 0, f"Claude {mode} failed; see {log.name}"
            finally:
                stop(process)
    result = json.loads((root / "session.json").read_text())
    result["route"] = provider.assert_route("claude-sonnet-4-6")
    return result
