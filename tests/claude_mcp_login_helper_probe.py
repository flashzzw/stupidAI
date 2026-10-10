#!/usr/bin/env python3
"""Compare a dynamic MCP helper with raw and cwd-wrapped login sidecars.

No model input, OAuth login or listening server. The unreachable endpoint only
exercises helper startup. Never restore credentials removed by the native CLI.
"""
import json
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile

from console_fixture import stop
from execution_config_probe import environment, verify_versions, write

REPO = Path(__file__).resolve().parents[1]


def main():
    root = Path(tempfile.mkdtemp(prefix='netcap-login-helper-'))
    print('Evidence directory:', root, flush=True)
    verify_versions(root, ['claude'])
    cases = {}
    checks = {}
    for letter in ('A', 'B'):
        for mode in ('dynamic', 'sidecar', 'wrapped'):
            key = letter + '-' + mode
            folder = root / key
            env = environment(folder)
            project = folder / "project with space ' quote"
            sidecar = folder / 'login'
            project.mkdir()
            sidecar.mkdir()
            cwd = project if mode == 'dynamic' else sidecar
            subprocess.run(['git', 'init', '--quiet', str(cwd)], env=env, check=True)
            marker = folder / 'helper.json'
            (project / 'resource.txt').write_text(letter)
            (project / 'helper.py').write_text('''import json,os,sys
from pathlib import Path
letter=sys.argv[2]
Path(sys.argv[1]).write_text(json.dumps({
 'cwd':os.getcwd(), 'resource':Path('resource.txt').read_text(),
 'credential_present':'NETCAP_HELPER_KEY' in os.environ,
 'credential_matches':os.environ.get('NETCAP_HELPER_KEY')=='fixture-'+letter,
 'config_dir':os.environ.get('CLAUDE_CONFIG_DIR')}))
print('{}')
''')
            helper = shlex.join([sys.executable, './helper.py', str(marker), letter])
            if mode == 'wrapped':
                helper = 'cd -- ' + shlex.quote(str(project)) + ' && ' + helper
            definition = {'mcpServers': {'identity': {
                'type': 'http', 'url': 'http://127.0.0.1:1/mcp',
                'headersHelper': helper,
            }}}
            source = folder / 'definition.json'
            write(source, definition)
            if mode != 'dynamic':
                write(sidecar / '.mcp.json', definition)
            # Isolated explicit trust keeps trust rejection distinct from cwd
            # and source-dependent credential removal. No user trust is edited.
            write(Path(env['CLAUDE_CONFIG_DIR']) / '.claude.json', {
                'projects': {str(cwd): {'hasTrustDialogAccepted': True}}})
            settings = folder / 'settings.json'
            write(settings, {'enableAllProjectMcpServers': True,
                             'env': {'NETCAP_HELPER_KEY': 'fixture-' + letter}})
            output = folder / 'result.json'
            write(folder / 'input.json', {
                'cwd': str(cwd), 'settings': str(settings),
                'definition': str(source), 'marker': str(marker),
                'source': 'dynamic' if mode == 'dynamic' else 'project',
                'mode': 'enabled', 'output': str(output),
            })
            proc = subprocess.Popen([
                'node', str(REPO / 'tests/claude_mcp_helper_probe.mjs'),
                str(folder / 'input.json'),
            ], cwd=cwd, env=env, stdout=subprocess.PIPE,
                stderr=subprocess.PIPE, text=True, start_new_session=True)
            try:
                proc.communicate(timeout=30)
                if proc.returncode != 0:
                    raise RuntimeError('native helper observation failed: ' + key)
            finally:
                stop(proc)
            observed = json.loads(output.read_text())
            invocation = json.loads(marker.read_text()) if marker.exists() else None
            cases[key] = {'native': observed, 'invocation': invocation}
            checks[key + '_no_model'] = observed['model_results'] == 0
            checks[key + '_trust_accepted'] = not observed['trust_warning']
            if mode == 'sidecar':
                checks[key + '_relative_path_not_resolved'] = invocation is None
            else:
                checks[key + '_original_cwd_and_resource'] = bool(invocation) and (
                    invocation['cwd'] == str(project) and invocation['resource'] == letter)
                checks[key + '_profile_bound'] = bool(invocation) and (
                    invocation['config_dir'] == env['CLAUDE_CONFIG_DIR'])
                checks[key + '_credential_rule'] = bool(invocation) and (
                    invocation['credential_present'] == (mode == 'dynamic') and
                    invocation['credential_matches'] == (mode == 'dynamic'))
    result = {
        'scope': 'native helper startup only; no OAuth, model input or authenticated request',
        'checks': checks, 'observations_match': all(checks.values()), 'cases': cases,
        'cwd_wrapper_preserves_dynamic_credentials': all(
            bool(cases[p + '-wrapped']['invocation']) and
            cases[p + '-wrapped']['invocation']['credential_matches'] for p in ('A', 'B')),
    }
    write(root / 'result.json', result)
    print(json.dumps(result, ensure_ascii=False, indent=2), flush=True)
    # Zero means the documented incompatibility was reproduced, not support.
    return 0 if result['observations_match'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
