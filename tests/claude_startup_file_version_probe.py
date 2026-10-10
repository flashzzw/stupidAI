#!/usr/bin/env python3
"""Fresh SDK samples must retain file and path versions even for identical bytes."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile

from execution_config_probe import environment, write


def main():
    root = Path(tempfile.mkdtemp(prefix='netcap-file-versions-'))
    print('Evidence directory:', root, flush=True)
    env = environment(root)
    env['WSL_DISTRO_NAME'] = 'netcap-owned-preflight'
    cwd = root / 'work'
    cwd.mkdir()
    user = Path(env['CLAUDE_CONFIG_DIR']) / 'settings.json'
    project, local, custom = cwd / '.claude/settings.json', cwd / '.claude/settings.local.json', root / 'custom.json'
    write(user, {'env': {'OWNED_STARTUP_SOURCE': 'PRIVATE_PREFLIGHT_SENTINEL'}})
    write(project, {'enabledPlugins': {'owned@local': False}})
    write(custom, {'env': {'CUSTOM_SOURCE': 'PRIVATE_PREFLIGHT_SENTINEL'}})
    policy = root / 'controlled-policy/managed-settings.json'
    write(policy, {'enabledPlugins': {'owned@managed': False}, 'env': {'OWNED_MANAGED_SOURCE': 'PRIVATE_PREFLIGHT_SENTINEL'}})
    body = policy.read_bytes()
    body_hash = hashlib.sha256(body).hexdigest()
    request = {'root': str(root), 'mode': 'managed-stable', 'cwd': str(cwd), 'user': str(user),
               'project': str(project), 'local': str(local), 'custom': str(custom),
               'helper_marker': str(root / 'helper-executed')}
    write(root / 'request.json', request)
    checks, samples = {}, {}

    def stat_snapshot(path):
        keys = ['st_dev', 'st_ino', 'st_mode', 'st_uid', 'st_gid', 'st_nlink', 'st_size', 'st_mtime_ns', 'st_ctime_ns']
        return {'resolved_path': str(path.resolve()), 'file': {k: getattr(path.stat(), k) for k in keys},
                'entry': {k: getattr(path.lstat(), k) for k in keys}}

    def sample(name):
        stage = root / 'samples' / name
        stage.mkdir(parents=True)
        before = stat_snapshot(policy)
        proc = subprocess.run(['node', str(Path(__file__).with_name('claude_startup_preflight_probe.mjs').resolve()), str(root / 'request.json')],
                              cwd=cwd, env=env, capture_output=True, text=True, timeout=25)
        write(stage / 'stdout.log', proc.stdout)
        write(stage / 'stderr.log', proc.stderr)
        result = json.loads((root / 'result.json').read_text())
        for filename in ['result.json', 'registry-replies.json', 'file-change.json']:
            (stage / filename).write_bytes((root / filename).read_bytes())
        report = result['report']
        binding = next((p for p in report.get('path_bindings', []) if p['path'] == '/etc/claude-code/managed-settings.json'), {})
        file = next((f for f in report['files'] if f['path'] == binding.get('resolved_path')), {})
        record = {'file': file, 'binding': binding, 'filesystem_before': before, 'filesystem_after': stat_snapshot(policy)}
        write(stage / 'versions.json', record)
        samples[name] = record
        checks[name + '_fresh_read'] = proc.returncode == 0 and result['passed'] and report['read_complete'] and not report['complete']
        checks[name + '_same_bytes'] = file.get('sha256') == body_hash and policy.read_bytes() == body
        checks[name + '_versions_observed'] = len(file.get('identity_sha256', '')) == 64 and len(binding.get('identity_sha256', '')) == 64
        checks[name + '_no_values_exported'] = 'PRIVATE_PREFLIGHT_SENTINEL' not in json.dumps(report)
        return record

    first = sample('first')
    unchanged = sample('unchanged')
    checks['unchanged_file_and_binding_versions'] = first['file'] == unchanged['file'] and first['binding'] == unchanged['binding']
    replacement = policy.with_suffix('.replacement')
    replacement.write_bytes(body)
    os.chmod(replacement, policy.stat().st_mode & 0o777)
    replacement.replace(policy)
    replaced = sample('replaced')
    checks['same_bytes_replacement_changes_version'] = replaced['file'].get('identity_sha256') != unchanged['file'].get('identity_sha256')
    checks['replacement_changed_actual_inode'] = replaced['filesystem_before']['file']['st_ino'] != unchanged['filesystem_before']['file']['st_ino']
    stable_again = sample('stable-again')
    checks['replacement_version_is_stable'] = stable_again['file'] == replaced['file'] and stable_again['binding'] == replaced['binding']
    os.chmod(policy, (policy.stat().st_mode & 0o777) ^ 0o040)
    chmod = sample('chmod')
    checks['mode_change_changes_version'] = chmod['file'].get('identity_sha256') != stable_again['file'].get('identity_sha256') and chmod['filesystem_before']['file']['st_mode'] != stable_again['filesystem_before']['file']['st_mode']
    target = policy.parent / 'targets/first.json'
    write(target, body.decode())
    policy.unlink()
    policy.symlink_to(target)
    linked = sample('linked')
    policy.unlink()
    policy.symlink_to(target)
    relinked = sample('relinked-same-target')
    checks['unchanged_target_retains_file_version'] = linked['file'] == relinked['file']
    checks['same_target_new_link_changes_binding'] = linked['binding'].get('identity_sha256') != relinked['binding'].get('identity_sha256')
    last = sample('last')
    checks['unchanged_link_binding_is_stable'] = last['file'] == relinked['file'] and last['binding'] == relinked['binding']
    result = {'checks': checks, 'passed': all(checks.values()), 'samples': samples, 'body_sha256': body_hash,
              'scope': 'fresh fixed SDK processes and production reader; owned files and controlled Windows registry replies; no CLI query'}
    write(root / 'version-result.json', result)
    print(json.dumps({'passed': result['passed'], 'checks': len(checks), 'failed': [k for k, v in checks.items() if not v]}, indent=2))
    raise SystemExit(0 if result['passed'] else 1)


if __name__ == '__main__':
    main()
