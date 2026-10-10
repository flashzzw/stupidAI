#!/usr/bin/env python3
"""Claude SDK Skill discovery/source experiments; never send model input."""
import json
from pathlib import Path
import subprocess
import tempfile

from execution_config_probe import environment, verify_versions, write
from runtime.common import stop


def run(root):
    verify_versions(root, ["claude"])
    reports = {}
    for mode in ["default", "flag-off", "project-off", "managed-off", "same-name", "directory-off", "matching-name-off", "sdk-list", "sdk-canonical", "same-key-off", "live-off"]:
        case = root / mode
        env = environment(case)
        cwd = case / "work"
        cwd.mkdir()
        user = Path(env["CLAUDE_CONFIG_DIR"]) / "skills/shared/SKILL.md"
        project = cwd / ".claude/skills/project/SKILL.md"
        if mode == "matching-name-off":
            user = Path(env["CLAUDE_CONFIG_DIR"]) / "skills/w0207-shared/SKILL.md"
        for path, name in [(user, "w0207-shared"), (project, "w0207-project")]:
            write(path, f"---\nname: {name}\ndescription: {name} fixture method\n---\nPRIVATE_BODY_NOT_FOR_INVENTORY\n")
        write(cwd / "CLAUDE.md", "W0207_PROJECT_RULE: preserve this rule.\n")
        settings = {"disableAllHooks": True}
        if mode in ("flag-off", "matching-name-off"):
            settings["skillOverrides"] = {"w0207-shared": "off"}
        if mode == "directory-off":
            settings["skillOverrides"] = {"shared": "off"}
        if mode == "project-off":
            write(cwd / ".claude/settings.json", {"skillOverrides": {"w0207-shared": "off"}})
        if mode == "same-name":
            write(cwd / ".claude/skills/shadow/SKILL.md", "---\nname: w0207-shared\ndescription: project shadow method\n---\nSHADOW_BODY_NOT_FOR_INVENTORY\n")
        if mode == "same-key-off":
            write(cwd / ".claude/skills/shared/SKILL.md", "---\nname: w0207-shadow\ndescription: project same-key method\n---\nSHADOW_BODY_NOT_FOR_INVENTORY\n")
            settings["skillOverrides"] = {"shared": "off"}
        config = {"cwd": str(cwd), "settings": settings, "output": str(case / "result.json")}
        if mode in ("sdk-list", "sdk-canonical"):
            config["skills"] = ["w0207-project" if mode == "sdk-list" else "project"]
        if mode == "live-off":
            config["change"] = {"skillOverrides": {"shared": "off"}}
        if mode == "managed-off":
            config["managed"] = {"skillOverrides": {"w0207-shared": "off"}}
        write(case / "request.json", config)
        proc = subprocess.Popen(["node", str(Path(__file__).with_suffix(".mjs")), str(case / "request.json")],
                                env=env, cwd=Path(__file__).resolve().parent.parent,
                                text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                start_new_session=True)
        try:
            _, stderr = proc.communicate(timeout=45)
        finally:
            stop(proc)
        write(case / "stderr.log", stderr)
        result = json.loads((case / "result.json").read_text()) if (case / "result.json").exists() else {}
        commands = [c for c in result.get("commands", []) if c["name"].startswith("w0207-")]
        refreshed = [c for c in result.get("refreshed", []) if c["name"].startswith("w0207-")]
        sources = [s for s in result.get("skills", []) if s["name"].startswith("w0207-")]
        # Assertions now encode the observed native key contract. Keep the
        # requested exclusion outcome separate: name-based failures do not
        # become successful exclusion merely because they are reproducible.
        hidden = mode in ("directory-off", "matching-name-off", "same-key-off")
        checks = {"native_query_completed": proc.returncode == 0 and bool(result),
                  "no_model_results": result.get("model_results") == 0,
                  "project_stays_discoverable": any(c["name"] == "w0207-project" for c in commands),
                  "directory_key_visibility_observed": any(c["name"] == "w0207-shared" for c in commands) != hidden,
                  "no_body_in_inventory": "BODY_NOT_FOR_INVENTORY" not in json.dumps(result)}
        if mode == "same-key-off":
            checks["same_key_hides_both_sources"] = not any(c["name"] in ("w0207-shared", "w0207-shadow") for c in commands)
        if mode == "sdk-canonical":
            checks["canonical_allowlist_context_only_project"] = [s["name"] for s in sources] == ["w0207-project"]
        if mode == "sdk-list":
            checks["declared_name_allowlist_excludes_both_context_entries"] = not sources
        if mode == "live-off":
            changed = result.get("changed", {})
            checks["live_override_hides_shared_command"] = not any(c["name"] == "w0207-shared" for c in changed.get("commands", []))
            checks["live_override_hides_shared_context"] = not any(s["name"] == "w0207-shared" for s in changed.get("skills", []))
            checks["live_override_preserves_project"] = any(s["name"] == "w0207-project" for s in changed.get("skills", []))
        reports[mode] = {"exit_code": proc.returncode, "commands": commands, "refreshed": refreshed, "sources": sources, "checks": checks,
                         "shared_command_hidden_at_start": not any(c["name"] == "w0207-shared" for c in commands),
                         "changed": result.get("changed"),
                         "expectations_met": all(checks.values())}
        write(root / "result.json", {"cases": reports, "scope": "fixed Claude SDK command inventory and local context estimates; no model input, production selection or method Gate"})
        print(mode, json.dumps(reports[mode], ensure_ascii=False), flush=True)
    return all(r["expectations_met"] for r in reports.values())


if __name__ == "__main__":
    root = Path(tempfile.mkdtemp(prefix="netcap-w0207-claude-skills-"))
    print("Evidence directory:", root, flush=True)
    raise SystemExit(0 if run(root) else 1)
