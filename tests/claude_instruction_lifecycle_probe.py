#!/usr/bin/env python3
"""Compare native instruction reports, load events and actual local requests."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile

from claude_skill_method_probe import SkillProvider, strings
from execution_config_probe import CLAUDE, environment, verify_versions, write
from runtime.common import stop

REPO = Path(__file__).resolve().parents[1]
MODES = ['rewrite-parent', 'rewrite-user', 'rewrite-import', 'rewrite-managed',
         'delete-import', 'add-rule', 'nested-read', 'startup-race', 'hook-race', 'hooks-disabled', 'hook-block']


class InstructionProvider(SkillProvider):
    def plan(self, payload):
        messages = payload.get('messages', [])
        phases = [(i, re.search(r'W0209_INSTRUCTION_CASE=(initial|hot|cold)', strings(m.get('content'))))
                  for i, m in enumerate(messages) if m.get('role') == 'user']
        phases = [(i, match.group(1)) for i, match in phases if match]
        if not phases:
            return {'text': 'No instruction diagnostic task.'}
        start, phase = phases[-1]
        if self.mode == 'nested-read' and phase == 'hot' and not any(
                m.get('role') == 'assistant' and any(b.get('type') == 'tool_use' for b in m.get('content', []) if isinstance(b, dict))
                for m in messages[start:]):
            return {'tool': 'Read', 'input': {'file_path': str(self.read_file)}}
        return {'text': 'W0209_INSTRUCTION_DONE_' + phase}


def request_observations(records):
    observations = []
    for row in records:
        body = row['body']
        phases = re.findall(r'W0209_INSTRUCTION_CASE=(initial|hot|cold)', strings(body.get('messages')))
        sources = sorted(set(re.findall(r'^Contents of ([^\n]+?)(?: \([^\n]+\))?:$', strings(body), re.MULTILINE)))
        observations.append({'phase': phases[-1] if phases else 'unknown', 'path': row['path'], 'turns': phases,
            'markers': sorted(set(re.findall(r'W0209_MEMORY_[A-Z_]+_V[12]', strings(body)))),
            'instruction_sources': sources, 'auth_matches': row['auth_matches']})
    return observations


def lifecycle_contracts(root, report):
    """Pin consumer-relevant limits to the fixed CLI's observed behavior."""
    request = json.loads((root / 'request.json').read_text())
    sdk = json.loads((root / 'sdk-result.json').read_text())
    mode, samples, loads = report['mode'], report['samples'], report['loads']
    files = request['files']
    expected = {files[name] for name in ['user', 'parent', 'project', 'import', 'rule', 'local']}
    expected.add('/etc/claude-code/CLAUDE.md')
    paths = lambda phase: {item['path'] for item in samples[phase]['memory_files']}
    phase = lambda name: [row for row in report['requests'] if row['phase'] == name]
    markers = lambda name: set(phase(name)[-1]['markers'])
    initial_markers = {'W0209_MEMORY_' + name + '_V1' for name in ['USER', 'PARENT', 'PROJECT', 'IMPORT', 'RULE', 'LOCAL', 'MANAGED']}
    checks = {
        'initial_report_has_seven_known_sources': paths('initialized') == expected,
        'native_four_source_labels': {item['type'] for item in samples['initialized']['memory_files']} == {'User', 'Project', 'Local', 'Managed'},
        'initial_request_has_expected_instruction_bytes': markers('initial') == initial_markers,
        'summary_reads_do_not_refresh_initial_memory': samples['initialized']['memory_files'] == samples['hot']['memory_files'],
        'load_events_keep_original_native_id': bool(loads) and all(item['session_id'] == sdk['native'] for item in loads),
        'import_event_preserves_parent_and_reason': any(item['file_path'] == files['import'] and item['load_reason'] == 'include' and item.get('parent_file_path') == files['project'] for item in loads),
        'resumed_request_retains_prior_turns': len(phase('cold')) == 1 and phase('cold')[0]['turns'] == ['initial', 'hot', 'cold'],
        'only_expected_model_rounds': len(report['requests']) == (4 if mode == 'nested-read' else 3),
        'metadata_has_no_instruction_content': 'W0209_MEMORY_' not in json.dumps([value['memory_files'] for value in samples.values()]),
        'requests_report_only_owned_instruction_paths': all(row['instruction_sources'] and all(Path(path).is_relative_to(root) or path == '/etc/claude-code/CLAUDE.md' for path in row['instruction_sources']) for row in report['requests']),
        'initial_request_sources_match_owned_fixtures': set(phase('initial')[0]['instruction_sources']) == expected,
    }
    rewritten = mode.removeprefix('rewrite-') if mode.startswith('rewrite-') else 'parent' if mode in ['startup-race', 'hook-race', 'hooks-disabled'] else None
    if rewritten:
        old, new = 'W0209_MEMORY_' + rewritten.upper() + '_V1', 'W0209_MEMORY_' + rewritten.upper() + '_V2'
        checks.update({
            'hot_query_uses_cached_original_bytes': old in markers('hot') and new not in markers('hot'),
            'resumed_request_contains_old_and_new_bytes': {old, new} <= markers('cold'),
            'same_metadata_does_not_prove_same_content': samples['initialized']['memory_files'] == samples['cold']['memory_files'],
            'owned_file_really_changed': request['initial_disk'][rewritten] != samples['cold']['disk'][rewritten],
        })
    if mode == 'startup-race':
        checks['initialized_file_changed_before_first_input'] = request['initial_disk']['parent'] != samples['initial']['disk']['parent']
    if mode == 'hook-race':
        checks['load_callback_changed_disk_before_initialization_returned'] = request['initial_disk']['parent'] != samples['initialized']['disk']['parent']
    if mode == 'delete-import':
        checks.update({
            'deleted_file_absent_on_disk_but_retained_in_hot_report': samples['after_disk_change']['disk']['import'] == 'absent' and files['import'] in paths('hot'),
            'cold_report_omits_deleted_file': files['import'] not in paths('cold'),
            'resumed_request_still_has_historical_deleted_content': 'W0209_MEMORY_IMPORT_V1' in markers('cold'),
        })
    if mode == 'add-rule':
        checks.update({
            'new_rule_not_loaded_by_hot_summary_or_input': files['added'] not in paths('hot') and 'W0209_MEMORY_ADDED_V2' not in markers('hot'),
            'new_rule_loaded_after_original_id_resume': files['added'] in paths('cold') and 'W0209_MEMORY_ADDED_V2' in markers('cold'),
        })
    if mode == 'nested-read':
        nested = 'W0209_MEMORY_NESTED_V1'
        checks.update({
            'nested_instruction_arrives_after_real_read': len(phase('hot')) == 2 and nested not in phase('hot')[0]['markers'] and nested in phase('hot')[1]['markers'],
            'nested_load_event_has_native_trigger': any(item['file_path'] == files['nested'] and item['load_reason'] == 'nested_traversal' and item.get('trigger_file_path') == request['read_file'] and item.get('prompt_id') for item in loads),
            'summary_omits_tool_triggered_nested_instruction': files['nested'] not in paths('hot') and files['nested'] not in paths('cold'),
            'nested_content_survives_in_resumed_history': nested in markers('cold'),
        })
    if mode == 'hooks-disabled':
        checks['native_reports_all_hooks_disabled'] = all(value['policy']['all_disabled'] for value in samples.values())
        checks['instruction_notifications_still_observed_with_disabled_hook_policy'] = len(loads) == 14
    if mode == 'hook-block':
        checks['sdk_block_output_does_not_exclude_instruction'] = all('W0209_MEMORY_PARENT_V1' in markers(name) for name in ['initial', 'hot', 'cold'])
    return checks


def inside(root, mode, prompt_correlation=False):
    verify_versions(root, ['claude'])
    env = environment(root / 'execution')
    parent = root / 'project'
    cwd = parent / 'nested/work'
    cwd.mkdir(parents=True)
    subprocess.run(['git', 'init', '--quiet', str(cwd)], check=True, env=env)
    files = {'user': Path(env['CLAUDE_CONFIG_DIR']) / 'CLAUDE.md', 'parent': parent / 'CLAUDE.md',
             'project': cwd / 'CLAUDE.md', 'import': cwd / 'reference.md',
             'rule': parent / '.claude/rules/owned.md', 'local': cwd / 'CLAUDE.local.md',
             'managed': root / 'policy/CLAUDE.md', 'nested': cwd / 'feature/CLAUDE.md',
             'added': parent / '.claude/rules/added.md'}
    for name, path in files.items():
        if name == 'added':
            continue
        write(path, 'W0209_MEMORY_' + name.upper() + '_V1\nFollow only this owned fixture.\n')
    write(files['project'], files['project'].read_text() + '@./reference.md\n')
    read_file = cwd / 'feature/data.txt'
    write(read_file, 'W0209_OWNED_FILE_RESULT\n')
    changes = []
    target = mode.removeprefix('rewrite-')
    if mode in ['startup-race', 'hook-race', 'hooks-disabled']:
        target = 'parent'
    if target in files and target not in ['nested', 'added']:
        changes = [{'action': 'write', 'path': str(files[target]), 'text': files[target].read_text().replace('_V1', '_V2')}]
    if mode == 'delete-import':
        changes = [{'action': 'delete', 'path': str(files['import'])}]
    if mode == 'add-rule':
        changes = [{'action': 'write', 'path': str(files['added']), 'text': 'W0209_MEMORY_ADDED_V2\nFollow only this owned fixture.\n'}]
    provider = InstructionProvider(root, 'A')
    provider.mode, provider.read_file = mode, read_file
    proc = None
    try:
        request = root / 'request.json'
        write(request, {'root': str(root), 'cwd': str(cwd), 'executable': CLAUDE,
                        'endpoint': provider.url, 'mode': mode, 'files': {k: str(v) for k, v in files.items()},
                        'read_file': str(read_file), 'changes': changes,
                        'prompt_correlation': prompt_correlation,
                        'initial_disk': {name: hashlib.sha256(path.read_bytes()).hexdigest() if path.exists() else 'absent' for name, path in files.items()},
                        'settings': {'disableAllHooks': True} if mode == 'hooks-disabled' else {}})
        proc = subprocess.Popen(['node', str(Path(__file__).with_suffix('.mjs')), str(request)], cwd=REPO,
                                env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
        out, err = proc.communicate(timeout=90)
        write(root / 'sdk.stdout', out)
        write(root / 'sdk.stderr', err)
        result = json.loads((root / 'sdk-result.json').read_text()) if (root / 'sdk-result.json').exists() else {'checks': {}}
        observations = request_observations(provider.requests)
        checks = result['checks']
        checks.update({'native_exit': proc.returncode == 0,
                       'three_successful_native_turns': sum(e.get('type') == 'result' and not e.get('is_error') for e in result.get('events', [])) == 3,
                       'no_token_count_requests': not any('count_tokens' in r['path'] for r in provider.requests),
                       'only_local_authenticated_requests': bool(provider.requests) and all(r['auth_matches'] for r in provider.requests) and not provider.errors,
                       'no_unrequested_tools': all(p['allow'] for p in result.get('permissions', []))})
        report = {'mode': mode, 'checks': checks, 'requests': observations,
                  'samples': result.get('samples', {}), 'loads': result.get('loads', []),
                  'scope': 'real fixed CLI; local scripted replies; observations do not prove a frozen loaded version'}
        if proc.returncode == 0:
            checks.update(lifecycle_contracts(root, report))
        write(root / 'result.json', report)
        print(json.dumps({'mode': mode, 'checks_passed': all(checks.values()), 'requests': observations,
                          'load_count': len(report['loads']), 'evidence': str(root)}))
        if not all(checks.values()):
            raise RuntimeError('native instruction lifecycle probe failed; see sdk.stderr and result.json')
    finally:
        if proc:
            stop(proc)
        provider.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--case', choices=MODES, default='rewrite-parent')
    parser.add_argument('--inside', type=Path)
    parser.add_argument('--audit', type=Path, help='Recheck saved native requests without starting a query')
    parser.add_argument('--prompt-correlation', action='store_true', help='Observe native prompt IDs with explicitly identified SDK inputs')
    args = parser.parse_args()
    if args.audit:
        root = args.audit.resolve()
        report = json.loads((root / 'result.json').read_text())
        records = [json.loads(path.read_text()) for path in sorted((root / 'provider-A').glob('request-*.json'), key=lambda path: int(path.stem.split('-')[-1]))]
        report['requests'] = request_observations(records)
        checks = lifecycle_contracts(root, report)
        write(root / 'request-audit.json', {'checks': checks, 'requests': report['requests']})
        print(json.dumps({'checks': len(checks), 'failed': [name for name, passed in checks.items() if not passed]}))
        raise SystemExit(0 if all(checks.values()) else 1)
    if args.inside:
        return inside(args.inside, args.case, args.prompt_correlation)
    root = Path(tempfile.mkdtemp(prefix='netcap-instruction-lifecycle-', dir='/var/tmp'))
    (root / 'policy').mkdir()
    print('Evidence directory:', root, flush=True)
    command = ['bwrap', '--unshare-user', '--unshare-pid', '--unshare-net', '--die-with-parent',
               '--ro-bind', '/', '/', '--tmpfs', '/etc', '--ro-bind', str(root / 'policy'), '/etc/claude-code',
               '--bind', str(root), str(root), '--tmpfs', '/mnt', '--tmpfs', '/tmp', '--tmpfs', '/run',
               '--proc', '/proc', '--dev', '/dev', sys.executable, str(Path(__file__).resolve()),
               '--inside', str(root), '--case', args.case]
    if args.prompt_correlation:
        command.append('--prompt-correlation')
    proc = subprocess.Popen(command, cwd=REPO, env=environment(root / 'environment'), text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    try:
        out, err = proc.communicate(timeout=105)
        write(root / 'runner.stdout', out)
        write(root / 'runner.stderr', err)
        print(out, end='')
        print(err, end='', file=sys.stderr)
    finally:
        stop(proc)
    raise SystemExit(proc.returncode)


if __name__ == '__main__':
    main()
