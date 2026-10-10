#!/usr/bin/env python3
"""Original versus private derived settings with real owned Read/Write calls.

Uses the production Session through a small Node runner and a local scripted
provider. This is not an Application installed-plugin selection transaction.
"""
import json
import argparse
import os
from pathlib import Path
import re
import subprocess
import tempfile

from claude_plugin_contract_probe import plugin, install, digest_tree
from execution_config_probe import environment, verify_versions, write
from runtime.common import Provider, stop


class AccessProvider(Provider):
    def __init__(self, root, cases):
        super().__init__(root, 'A')
        self.cases = {case['name']: case for case in cases}
        self.stages = {}

    def anthropic(self, payload, answer, seq):
        markers = re.findall(r'NETCAP_SETTINGS_ACCESS_(original|composed)_([a-z-]+)',
                             json.dumps(payload.get('messages', [])))
        if not markers:
            raise AssertionError('unexpected model request without fixture marker')
        stage, name = markers[-1]
        key = stage + ':' + name
        count = self.stages.get(key, 0)
        self.stages[key] = count + 1
        if count:
            return super().anthropic(payload, answer, seq)
        case = self.cases[name]
        message = {'id': f'msg_{seq}', 'type': 'message', 'role': 'assistant', 'model': payload['model'],
                   'content': [], 'stop_reason': None, 'stop_sequence': None,
                   'usage': {'input_tokens': 32, 'output_tokens': 0}}
        return self.sse([
            ('message_start', {'message': message}),
            ('content_block_start', {'index': 0, 'content_block': {'type': 'tool_use', 'id': f'tool_{seq}',
                                                                'name': case['tool'], 'input': {}}}),
            ('content_block_delta', {'index': 0, 'delta': {'type': 'input_json_delta', 'partial_json': json.dumps(case['input'])}}),
            ('content_block_stop', {'index': 0}),
            ('message_delta', {'delta': {'stop_reason': 'tool_use', 'stop_sequence': None}, 'usage': {'output_tokens': 32}}),
            ('message_stop', {}),
        ])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--anchors', action='store_true', help='Also exercise single-slash rules anchored at the custom settings directory.')
    parser.add_argument('--placement', choices=['private-directory', 'beside-original'], default='private-directory',
                        help='Keep the composed file in a separate directory or the original settings directory.')
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-derived-access-'))
    print('Evidence directory:', root, flush=True)
    verify_versions(root, ['claude'])
    env = environment(root)
    cwd, custom_dir, private = root / 'work', root / 'custom/location', root / 'private/settings'
    for directory in [cwd, custom_dir, private, root / 'extra']:
        directory.mkdir(parents=True, exist_ok=True)
    private.parent.chmod(0o700)
    private.chmod(0o700)
    custom_dir.chmod(0o700)
    contents = {
        cwd / 'allowed/read.txt': 'OWNED_ALLOWED_READ\n',
        cwd / 'secret/read.txt': 'OWNED_CWD_DENIED_CONTENT\n',
        cwd / 'project-secret/read.txt': 'OWNED_PROJECT_DENIED_CONTENT\n',
        root / 'extra/read.txt': 'OWNED_EXTRA_READ\n',
        custom_dir / 'secret/read.txt': 'OWNED_ORIGINAL_DIRECTORY_READ\n',
        private / 'secret/read.txt': 'OWNED_DERIVED_DIRECTORY_READ\n',
        cwd / 'blocked/write.txt': 'OWNED_WRITE_MUST_STAY_UNCHANGED\n',
    }
    if args.anchors:
        contents.update({custom_dir / 'anchored/read.txt': 'OWNED_ORIGINAL_ANCHORED_DENIED\n',
                         custom_dir / 'anchored/write.txt': 'OWNED_ANCHORED_WRITE_MUST_STAY_UNCHANGED\n',
                         private / 'anchored/read.txt': 'OWNED_DERIVED_ANCHORED_READ\n'})
    for path, body in contents.items():
        write(path, body)
    (cwd / 'scratch').mkdir()
    write(cwd / 'local-hook.py', 'import os,json\nfrom pathlib import Path\nwith Path(os.environ["NETCAP_SETTINGS_ACCESS_LOG"]).open("a") as f:f.write(json.dumps({"source":"cwd","cwd":str(Path.cwd()),"marker":os.environ.get("NETCAP_CUSTOM_SETTINGS_SENTINEL")})+"\\n")\n')
    source = root / 'marketplace/selected'
    plugin(source, 'w0208-selected')
    write(source / 'hooks/hooks.json', {'hooks': {'SessionStart': [{'hooks': [{
        'type': 'command', 'command': 'python3 "' + str(source / 'startup.py') + '"', 'timeout': 5}]}]}})
    write(source / 'startup.py', 'import os,json\nfrom pathlib import Path\nwith Path(os.environ["NETCAP_SETTINGS_ACCESS_LOG"]).open("a") as f:f.write(json.dumps({"source":"installed","root":os.environ.get("CLAUDE_PLUGIN_ROOT")})+"\\n")\n')
    install(root, env, cwd)
    write(cwd / '.claude/settings.json', {'permissions': {'deny': ['Read(./project-secret/**)']}})
    settings = {
        'env': {'NETCAP_CUSTOM_SETTINGS_SENTINEL': 'custom-preserved'},
        'enabledPlugins': {'w0208-preserved@w0208-local': False},
        'permissions': {'allow': ['Read(./allowed/**)'], 'deny': ['Read(./secret/**)', 'Edit(./blocked/**)'],
                        'ask': ['Write'], 'additionalDirectories': ['../extra']},
        'sandbox': {'enabled': False},
        'hooks': {'SessionStart': [{'hooks': [{'type': 'command', 'command': 'python3 ./local-hook.py', 'timeout': 5}]}]},
    }
    if args.anchors:
        settings['permissions']['deny'] += ['Read(/anchored/**)', 'Edit(/anchored/**)']
    original, composed = custom_dir / 'original.json', private / 'composed.json'
    if args.placement == 'beside-original':
        fd, name = tempfile.mkstemp(prefix='.netcap-settings-', suffix='.json', dir=custom_dir)
        os.close(fd)
        composed = Path(name)
    write(original, settings)
    write(composed, {**settings, 'enabledPlugins': {**settings['enabledPlugins'], 'w0208-selected@w0208-local': False}})
    composed.chmod(0o600)
    cases = [
        {'name': 'read-allowed', 'tool': 'Read', 'input': {'file_path': str(cwd / 'allowed/read.txt')}, 'expected': 'success', 'marker': 'OWNED_ALLOWED_READ'},
        {'name': 'read-denied', 'tool': 'Read', 'input': {'file_path': str(cwd / 'secret/read.txt')}, 'expected': 'denied'},
        {'name': 'read-project-denied', 'tool': 'Read', 'input': {'file_path': str(cwd / 'project-secret/read.txt')}, 'expected': 'denied'},
        {'name': 'read-extra', 'tool': 'Read', 'input': {'file_path': str(root / 'extra/read.txt')}, 'expected': 'success', 'marker': 'OWNED_EXTRA_READ'},
        {'name': 'read-original-directory', 'tool': 'Read', 'input': {'file_path': str(custom_dir / 'secret/read.txt')}, 'expected': 'success', 'marker': 'OWNED_ORIGINAL_DIRECTORY_READ'},
        {'name': 'read-derived-directory', 'tool': 'Read', 'input': {'file_path': str(private / 'secret/read.txt')}, 'expected': 'success', 'marker': 'OWNED_DERIVED_DIRECTORY_READ'},
        {'name': 'write-allowed', 'tool': 'Write', 'input': {'file_path': str(cwd / 'scratch/write.txt'), 'content': 'OWNED_APPROVED_WRITE\n'}, 'expected': 'success'},
        {'name': 'write-denied', 'tool': 'Write', 'input': {'file_path': str(cwd / 'blocked/new.txt'), 'content': 'UNEXPECTED_WRITE\n'}, 'expected': 'denied', 'absent': True},
    ]
    if args.anchors:
        cases += [
            {'name': 'read-anchor-original', 'tool': 'Read', 'input': {'file_path': str(custom_dir / 'anchored/read.txt')}, 'expected': 'denied'},
            {'name': 'read-anchor-derived', 'tool': 'Read', 'input': {'file_path': str(private / 'anchored/read.txt')}, 'expected': 'success', 'marker': 'OWNED_DERIVED_ANCHORED_READ'},
            {'name': 'write-anchor-original', 'tool': 'Write', 'input': {'file_path': str(custom_dir / 'anchored/new.txt'), 'content': 'UNEXPECTED_ANCHORED_WRITE\n'}, 'expected': 'denied', 'absent': True},
            {'name': 'write-anchor-derived', 'tool': 'Write', 'input': {'file_path': str(private / 'anchored/write.txt'), 'content': 'OWNED_APPROVED_ANCHORED_WRITE\n'}, 'expected': 'success'},
        ]
    originals = {str(path): path.read_bytes() for path in [original, composed, cwd / '.claude/settings.json',
                  Path(env['CLAUDE_CONFIG_DIR']) / 'settings.json',
                  Path(env['CLAUDE_CONFIG_DIR']) / 'plugins/installed_plugins.json',
                  Path(env['CLAUDE_CONFIG_DIR']) / 'plugins/known_marketplaces.json']}
    source_before = digest_tree(source)
    provider = AccessProvider(root, cases)
    write(root / 'request.json', {'root': str(root), 'cwd': str(cwd), 'original': str(original),
          'composed': str(composed), 'endpoint': provider.url, 'provider_log': str(provider.root), 'cases': cases,
          'anchors': args.anchors, 'placement': args.placement})
    proc = None
    try:
        proc = subprocess.Popen(['node', str(Path(__file__).with_suffix('.mjs').resolve()), str(root / 'request.json')],
                                cwd=cwd, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
        stdout, stderr = proc.communicate(timeout=180)
        write(root / 'runner.stdout.log', stdout)
        write(root / 'runner.stderr.log', stderr)
    finally:
        stop(proc)
        provider.close()
    result = json.loads((root / 'session-result.json').read_text())
    checks = result['checks']
    checks['runner_exit_zero'] = proc.returncode == 0
    checks['original_sources_and_private_settings_unchanged'] = all(Path(p).read_bytes() == body for p, body in originals.items()) and digest_tree(source) == source_before
    checks['owned_read_files_and_denied_write_unchanged'] = all(path.read_text() == body for path, body in contents.items())
    checks['denied_files_not_created'] = all(not Path(case['input']['file_path']).exists() for case in cases if case.get('absent'))
    checks['private_directory_and_file_modes'] = composed.parent.stat().st_mode & 0o777 == 0o700 and composed.stat().st_mode & 0o777 == 0o600
    checks['provider_binding_and_no_errors'] = bool(provider.requests) and all(r['auth_matches'] for r in provider.requests) and not provider.errors
    serialized = json.dumps(provider.requests)
    checks['denied_file_contents_never_reach_provider'] = all(marker not in serialized for marker in ['OWNED_CWD_DENIED_CONTENT', 'OWNED_PROJECT_DENIED_CONTENT', 'OWNED_ORIGINAL_ANCHORED_DENIED'])
    for stage, report in result['reports'].items():
        for name, case in report.get('cases', {}).items():
            returned = [block for row in provider.requests for message in row['body'].get('messages', [])
                        for block in (message.get('content', []) if isinstance(message.get('content'), list) else [])
                        if isinstance(block, dict) and block.get('type') == 'tool_result' and block.get('tool_use_id') == case['tool_use_id']]
            # Native serialization can trim trailing whitespace and append
            # context reminders/cache hints; the original result body remains.
            native_body = case['tool_result']['content']
            checks[stage + '_' + name + '_native_result_body_reaches_provider'] = bool(returned) and all(
                bool(block.get('is_error')) == bool(case['tool_result'].get('is_error')) and
                isinstance(native_body, str) and isinstance(block.get('content'), str) and
                block['content'].startswith(native_body.rstrip()) for block in returned)
    result.update(scope='production Session + fixed native CLI with local scripted provider; original and fixture-derived settings under one native ID; owned Read/Write calls only',
                  expectations_met=all(checks.values()), provider_requests=len(provider.requests), anchors=args.anchors, placement=args.placement,
                  limitations=['not an Application installed-plugin selection/apply transaction',
                               'sandbox disabled; no filesystem sandbox enforcement claim',
                               'managed/remote/helper and concurrent source changes are not covered'])
    write(root / 'result.json', result)
    print(json.dumps({'checks': checks, 'expectations_met': result['expectations_met'], 'provider_requests': len(provider.requests)}, indent=2))
    raise SystemExit(0 if result['expectations_met'] else 1)


if __name__ == '__main__':
    main()
