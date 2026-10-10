#!/usr/bin/env python3
"""Compare pinned SDK/native settings roots with no model input; only owned startup marker hooks."""
import json
from pathlib import Path
import subprocess
import shlex
import sys
import time
import tempfile

from claude_plugin_contract_probe import Native, digest_tree
from execution_config_probe import environment, verify_versions, write


def markers(settings):
    return {k: v for k, v in settings.get('env', {}).items() if k.startswith('NETCAP_ROOTS_')}


def main():
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-settings-roots-'))
    print('Evidence directory:', root, flush=True)
    verify_versions(root, ['claude'])
    env = environment(root)
    repo = root / 'repo'
    child = repo / 'nested'
    child.mkdir(parents=True)
    subprocess.run(['git', 'init', '--quiet', str(repo)], env={**env, 'GIT_CONFIG_GLOBAL': '/dev/null', 'GIT_CONFIG_NOSYSTEM': '1'}, check=True, timeout=10)
    write(repo / '.claude/settings.json', {'env': {'NETCAP_ROOTS_PROJECT': 'repo'}})
    hook = root / 'startup-marker.py'
    write(hook, 'import json,os,sys\nfrom pathlib import Path\nwith Path(os.environ["NETCAP_ROOTS_TRACE"]).open("a") as f:f.write(json.dumps({"cwd":os.getcwd(),"marker":os.environ.get("NETCAP_ROOTS_LOCAL")})+"\\n")\n')
    write(repo / '.claude/settings.local.json', {
        'env': {'NETCAP_ROOTS_LOCAL': 'repo', 'NETCAP_ROOTS_REPO_ONLY': 'present'},
        'hooks': {'SessionStart': [{'hooks': [{'type': 'command', 'command': shlex.quote(sys.executable) + ' ' + shlex.quote(str(hook)), 'timeout': 5}]}]},
    })
    write(child / '.claude/settings.json', {'env': {'NETCAP_ROOTS_PROJECT': 'nested'}})
    write(child / '.claude/settings.local.json', {'env': {'NETCAP_ROOTS_LOCAL': 'nested', 'NETCAP_ROOTS_CHILD_ONLY': 'present'}})
    git_env = {**env, 'GIT_CONFIG_GLOBAL': '/dev/null', 'GIT_CONFIG_NOSYSTEM': '1'}
    subprocess.run(['git', '-C', str(repo), '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '--allow-empty', '-m', 'settings fixture'], env=git_env, check=True, capture_output=True, timeout=10)
    worktree = root / 'worktree'
    subprocess.run(['git', '-C', str(repo), 'worktree', 'add', '--detach', str(worktree)], env=git_env, check=True, capture_output=True, timeout=10)
    wt_child = worktree / 'nested'
    ordinary = root / 'ordinary'
    for directory in [worktree, wt_child, ordinary]:
        directory.mkdir(parents=True, exist_ok=True)
    write(worktree / '.claude/settings.local.json', {'env': {'NETCAP_ROOTS_LOCAL': 'worktree', 'NETCAP_ROOTS_WORKTREE_ONLY': 'present'}})
    write(wt_child / '.claude/settings.local.json', {'env': {'NETCAP_ROOTS_LOCAL': 'worktree-nested', 'NETCAP_ROOTS_WT_CHILD_ONLY': 'present'}})
    write(ordinary / '.claude/settings.local.json', {'env': {'NETCAP_ROOTS_LOCAL': 'ordinary'}})
    before = {str(p): digest_tree(p) for p in [repo, worktree, ordinary]}
    reports = {}
    checks = {}
    for label, cwd in [('root', repo), ('nested', child), ('worktree-root', worktree), ('worktree-nested', wt_child), ('ordinary', ordinary)]:
        case = root / 'cases' / label
        case.mkdir(parents=True)
        write(case / 'request.json', {'cwd': str(cwd), 'output': str(case / 'sdk.json'), 'trace': str(case / 'startup.jsonl')})
        sdk_proc = subprocess.run(['node', str(Path(__file__).with_suffix('.mjs').resolve()), str(case / 'request.json')], cwd=cwd, env=env, capture_output=True, text=True, timeout=20)
        write(case / 'sdk.stderr.log', sdk_proc.stderr)
        if sdk_proc.returncode:
            raise RuntimeError('SDK resolver process failed; see isolated stderr')
        trace = case / 'startup.jsonl'
        native = Native(case, {**env, 'NETCAP_ROOTS_TRACE': str(trace)}, cwd)
        report = {}
        try:
            native.call('initialize')
            value = native.call('get_settings')
            if label != 'ordinary':
                deadline = time.monotonic() + 5
                while not trace.exists() and time.monotonic() < deadline:
                    time.sleep(0.02)
            report = {'markers': markers(value.get('effective', {})), 'sources': [
                {'source': s.get('source'), 'markers': markers(s.get('settings', {}))} for s in value.get('sources', [])]}
        finally:
            native.close()
            report['exit_code'] = native.proc.returncode
            report['native_id'] = native.id
            report['pid'] = native.proc.pid
            report['startup'] = [json.loads(line) for line in trace.read_text().splitlines()] if trace.exists() else []
        sdk = json.loads((case / 'sdk.json').read_text())
        wire = json.loads((case / 'wire.json').read_text())
        report['hook_events'] = [{k: m.get(k) for k in ['subtype', 'hook_id', 'session_id', 'exit_code', 'outcome'] if k in m} for m in wire['incoming'] if m.get('subtype') in ['hook_started', 'hook_response']]
        checks[label + '_hook_terminal'] = (not report['hook_events']) if label == 'ordinary' else any(e.get('subtype') == 'hook_response' and e.get('exit_code') == 0 for e in report['hook_events'])
        reports[label] = {'cwd': str(cwd), 'sdk': sdk, 'native': report}
        checks[label + '_actual_startup_source'] = report['startup'] == ([] if label == 'ordinary' else [{'cwd': str(cwd), 'marker': 'repo'}])
        checks[label + '_sdk_did_not_execute_hook'] = not sdk.get('hook_executed', True)
        checks[label + '_same_markers'] = sdk['markers'] == report['markers']
        checks[label + '_no_input'] = all(x['type'] == 'control_request' for x in wire['outgoing'])
        checks[label + '_no_model_result'] = all(x.get('type') != 'result' for x in wire['incoming'])
        checks[label + '_process_terminal'] = native.proc.returncode is not None
    checks['owned_sources_unchanged'] = all(stamp == digest_tree(Path(path)) for path, stamp in before.items())
    report = {'checks': checks, 'reports': reports, 'passed': all(checks.values()), 'scope': 'real native CLI metadata and owned SessionStart marker vs real SDK with controlled absent registry; no query input, model or tool'}
    write(root / 'result.json', report)
    print(json.dumps(report, indent=2))
    raise SystemExit(0 if report['passed'] else 1)


if __name__ == '__main__':
    main()
