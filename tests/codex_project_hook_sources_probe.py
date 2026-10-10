#!/usr/bin/env python3
"""Measure project hook roots through fixed native metadata, never execute hooks."""
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import sys

from codex_scoped_hook_selection_probe import BASE_CONFIG
from codex_skill_selection_probe import Stdio
from execution_config_probe import SERVER, environment, verify_versions, write


def run(root):
    verify_versions(root, ['server'])
    env = environment(root)
    work = root / 'work'
    locations = [work, work / 'outer', work / 'outer/repo', work / 'outer/repo/nested', work / 'outer/repo/nested/leaf']
    cwd = locations[-1]
    cwd.mkdir(parents=True)
    profile = Path(env['CODEX_HOME'])
    config = profile / 'config.toml'
    files = [profile / 'hooks.json', *(p / '.codex/hooks.json' for p in locations)]
    for n, path in enumerate(files):
        write(path, {'hooks': {'PreToolUse': [{'matcher': 'Owned' + str(n), 'hooks': [{'type': 'command', 'command': 'true', 'timeout': 1}]}]}})
    trusted = ''.join('\n[projects.' + json.dumps(str(p)) + ']\ntrust_level="trusted"\n' for p in locations)
    hashes = {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
    report = {'root': str(root), 'locations': [str(p) for p in locations], 'source_hashes': hashes,
              'samples': {}, 'processes': [], 'binary_sha256': hashlib.sha256(Path(SERVER).read_bytes()).hexdigest(),
              'scope': 'owned fixed-version hooks/list and config/read, no thread/turn/model/hook execution'}
    for phase in ['plain', 'git-root', 'nested-git', 'worktree', 'worktree-no-root-folder', 'worktree-first', 'custom-marker', 'untrusted', 'missing-folder', 'missing-hook']:
        if phase in ['git-root', 'nested-git']:
            target = locations[2 if phase == 'git-root' else 3]
            subprocess.run(['git', 'init', '--quiet', str(target)], env={**env, 'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_GLOBAL': '/dev/null'}, check=True, timeout=10)
        extra_cwd = None
        if phase == 'worktree':
            git_env = {**env, 'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_GLOBAL': '/dev/null',
                       'GIT_AUTHOR_NAME': 'Owned fixture', 'GIT_AUTHOR_EMAIL': 'fixture@example.invalid',
                       'GIT_COMMITTER_NAME': 'Owned fixture', 'GIT_COMMITTER_EMAIL': 'fixture@example.invalid'}
            # This commit is only an empty fixture in the temporary repository.
            subprocess.run(['git', '-C', str(locations[2]), 'commit', '--allow-empty', '--quiet', '-m', 'Owned worktree fixture'], env=git_env, check=True, timeout=10)
            linked = work / 'linked'
            subprocess.run(['git', '-C', str(locations[2]), 'worktree', 'add', '--quiet', '--detach', str(linked)], env=git_env, check=True, timeout=10)
            extra_cwd = linked / 'child'
            for n, p in enumerate([linked, extra_cwd]):
                write(p / '.codex/hooks.json', {'hooks': {'PreToolUse': [{'matcher': 'Linked' + str(n), 'hooks': [{'type': 'command', 'command': 'true', 'timeout': 1}]}]}})
            report['worktree_cwd'] = str(extra_cwd)
        if phase in ['worktree-no-root-folder', 'worktree-first']:
            extra_cwd = Path(report['worktree_cwd'])
            linked_file = work / 'linked/.codex/hooks.json'
            if phase == 'worktree-no-root-folder':
                linked_body = linked_file.read_text()
                linked_file.unlink()
                linked_file.parent.rmdir()
            else:
                write(linked_file, linked_body)
        prefix = ''
        if phase == 'custom-marker':
            write(locations[1] / '.owned-root', '')
            prefix = 'project_root_markers=[".owned-root"]\n'
        write(config, prefix + BASE_CONFIG + ('' if phase == 'untrusted' else trusted) +
              ('\n[projects.' + json.dumps(str(work / 'linked')) + ']\ntrust_level="trusted"\n' if extra_cwd else ''))
        if phase == 'missing-folder':
            (cwd / '.codex/hooks.json').unlink()
            (cwd / '.codex').rmdir()
        if phase == 'missing-hook':
            (cwd / '.codex').mkdir()
        client = Stdio(root, env, phase)
        sample = {}
        report['samples'][phase] = sample
        try:
            if phase == 'worktree-first':
                sample['first_worktree_catalog'] = client.call('hooks/list', {'cwds': [str(extra_cwd)]})
            sample['catalog'] = client.call('hooks/list', {'cwds': [str(p) for p in locations]})
            sample['config'] = client.call('config/read', {'cwd': str(cwd), 'includeLayers': True})
            if extra_cwd:
                sample['worktree_config'] = client.call('config/read', {'cwd': str(extra_cwd), 'includeLayers': True})
                sample['worktree_catalog'] = client.call('hooks/list', {'cwds': [str(extra_cwd)]})
            if phase == 'missing-hook':
                write(cwd / '.codex/hooks.json', {'hooks': {'PreToolUse': [{'matcher': 'Owned5', 'hooks': [{'type': 'command', 'command': 'true', 'timeout': 1}]}]}})
                sample['added_config'] = client.call('config/read', {'cwd': str(cwd), 'includeLayers': True})
                sample['added_catalog'] = client.call('hooks/list', {'cwds': [str(cwd)]})
        finally:
            client.close()
            report['processes'].append({'phase': phase, 'pid': client.proc.pid, 'exit_code': client.proc.returncode})
            sample['metadata_only'] = all(c['method'] in ['initialize', 'hooks/list', 'config/read'] for c in client.calls) and not any(e.get('method','').startswith(('thread/', 'turn/', 'hook/', 'item/')) for e in client.events)
            write(root / 'result.json', report)
    report['sources_unchanged'] = hashes == {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
    checks = {}
    def check(name, value):
        checks[name] = bool(value)
    def project_paths(catalog):
        return [h['sourcePath'] for h in catalog['data'][-1]['hooks'] if h['source'] == 'project']
    def layers(config):
        return [l for l in config['layers'] if l['name']['type'] == 'project']
    expected = {'plain': [cwd], 'git-root': locations[2:], 'nested-git': locations[3:],
                'worktree': locations[3:], 'worktree-no-root-folder': locations[3:], 'worktree-first': locations[3:],
                'custom-marker': locations[1:], 'untrusted': [],
                'missing-folder': [locations[3]], 'missing-hook': [locations[3]]}
    for phase, sample in report['samples'].items():
        check(phase + '_exact_project_sources', project_paths(sample['catalog']) == [str(p / '.codex/hooks.json') for p in expected[phase]])
        expected_layers = locations[3:] if phase in ['untrusted', 'missing-hook'] else expected[phase]
        actual_layers = layers(sample['config'])
        check(phase + '_exact_configuration_layers', [l['name']['dotCodexFolder'] for l in actual_layers] == [str(p / '.codex') for p in reversed(expected_layers)])
        check(phase + '_layer_trust', all(bool(l.get('disabledReason')) == (phase == 'untrusted') for l in actual_layers))
        check(phase + '_complete_catalogs', all(not e['warnings'] and not e['errors'] for key, value in sample.items() if 'catalog' in key for e in value['data']))
        check(phase + '_metadata_only', sample['metadata_only'])
        if 'worktree_catalog' in sample:
            check(phase + '_main_repo_source', project_paths(sample['worktree_catalog']) == ([] if phase == 'worktree-no-root-folder' else [str(locations[2] / '.codex/hooks.json')]))
            check(phase + '_worktree_layers', [l['name']['dotCodexFolder'] for l in layers(sample['worktree_config'])] == [str(Path(report['worktree_cwd']) / '.codex')] + ([] if phase == 'worktree-no-root-folder' else [str(work / 'linked/.codex')]))
    check('worktree_query_order_independent', report['samples']['worktree-first']['first_worktree_catalog'] == report['samples']['worktree-first']['worktree_catalog'])
    added = report['samples']['missing-hook']
    check('added_hook_discovered_same_process', project_paths(added['added_catalog']) == [str(p / '.codex/hooks.json') for p in locations[3:]])
    check('adding_hook_keeps_config_layers', layers(added['added_config']) == layers(added['config']))
    check('original_source_bytes_restored', report['sources_unchanged'])
    check('owned_processes_exited', all(p['exit_code'] == 0 for p in report['processes']))
    report.update(checks=checks, expectations_met=all(checks.values()))
    write(root / 'result.json', report)
    return report


if __name__ == '__main__':
    root = Path(tempfile.mkdtemp(prefix='netcap-w0209-project-hook-sources-'))
    print('Evidence directory:', root, flush=True)
    report = run(root)
    print(json.dumps({'checks': report['checks'], 'expectations_met': report['expectations_met']}, indent=2))
    sys.exit(0 if report['expectations_met'] else 1)
