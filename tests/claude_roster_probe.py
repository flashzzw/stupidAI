#!/usr/bin/env python3
"""Read an owned live Claude TUI through the production roster observer.

Only a synthetic API-key setup prompt is accepted. No model prompt, native
attach/resume, listener or real provider; the original owned TUI is closed last.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import uuid

from execution_config_probe import CLAUDE, environment, verify_versions, write
from workbench_boundary_probe import Terminal

REPO = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    output = args.output.resolve()
    root = Path(tempfile.mkdtemp(prefix='netcap-w0104-live-'))
    env = environment(root)
    cwd = root / 'work'
    cwd.mkdir()
    profile = Path(env['CLAUDE_CONFIG_DIR'])
    native_id = str(uuid.uuid4())
    verify_versions(root, ['claude'])
    env.update(ANTHROPIC_BASE_URL='http://127.0.0.1:1', ANTHROPIC_API_KEY='fixture-no-provider')
    write(profile / '.claude.json', {'hasCompletedOnboarding': True, 'theme': 'dark',
                                    'projects': {str(cwd): {'hasTrustDialogAccepted': True}}})
    terminal = Terminal([CLAUDE, '--session-id', native_id, '--tools', ''], env, cwd, root / 'terminal.ansi')
    result = {'root': str(root), 'native_id': native_id, 'model_prompt_sent': False}
    try:
        terminal.drain(3)
        assert 'DetectedacustomAPIkey' in terminal.text(), 'expected isolated fixture key prompt'
        terminal.send('\x1b[A\r')
        result['setup_keys_sent'] = ['up', 'enter']
        terminal.drain(3)
        result['terminal_alive_before'] = terminal.proc.poll() is None
        scope = {'Binary': str(Path(CLAUDE).resolve()), 'Profile': str(profile), 'Home': str(root),
                 'Cwd': str(cwd), 'NativeID': native_id, 'PID': terminal.proc.pid,
                 'Output': str(output.with_name(output.stem + '-runtime.json'))}
        testing = subprocess.run(['go', 'test', '-race', './internal/runtime', '-run',
                                  '^TestClaudeNativeLiveRosterScope$', '-count=1', '-v'], cwd=REPO,
                                 env={**os.environ, 'GOCACHE': '/tmp/netcap-w0206-auth-go-cache',
                                      'NETCAP_NATIVE_CLAUDE_ROSTER_LIVE_SCOPE': json.dumps(scope)},
                                 capture_output=True, text=True, timeout=60)
        write(output.with_name(output.stem + '-runtime.txt'), testing.stdout + testing.stderr)
        query = subprocess.run([CLAUDE, 'agents', '--json', '--all', '--cwd', str(cwd)],
                               env=env, cwd=cwd, capture_output=True, text=True, timeout=12)
        rows = json.loads(query.stdout)
        result.update(runtime_test_exit=testing.returncode, query_exit=query.returncode,
                      roster=rows, terminal_alive_after=terminal.proc.poll() is None)
        matches = [row for row in rows if row.get('sessionId') == native_id]
        result['checks'] = {
            'runtime_query_passed': testing.returncode == 0,
            'cli_query_passed': query.returncode == 0,
            'same_live_pid_and_full_uuid': len(matches) == 1 and matches[0].get('pid') == terminal.proc.pid,
            'interactive_category': len(matches) == 1 and matches[0].get('kind') == 'interactive',
            'original_terminal_survives_query': result['terminal_alive_before'] and result['terminal_alive_after'],
            'no_supervisor_roster_created': not (profile / 'daemon/roster.json').exists(),
        }
        result['expectations_met'] = all(result['checks'].values())
    except Exception as error:
        result.update(error=str(error), expectations_met=False)
    finally:
        terminal.close()
        result['owned_terminal_exit'] = terminal.proc.returncode
        write(output, result)
        print(json.dumps(result, ensure_ascii=False, indent=2), flush=True)
    return 0 if result['expectations_met'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
