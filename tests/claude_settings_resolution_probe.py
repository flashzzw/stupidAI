#!/usr/bin/env python3
"""Isolated startup provenance diagnostics; no Claude query, plugin or model.

Default cases control registry failures without calling Windows. --host makes
read-only registry attempts through the real SDK and returns no policy values.
"""
import argparse
import json
from pathlib import Path
import subprocess
import tempfile

from execution_config_probe import environment, write
from runtime.common import stop


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--host', action='store_true')
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-settings-resolution-'))
    print('Evidence directory:', root, flush=True)
    results = {}
    for mode in ['host'] if args.host else ['absent', 'denied', 'unavailable', 'helper', 'malformed']:
        case = root / mode
        env = environment(case)
        cwd = case / 'work'
        cwd.mkdir()
        write(Path(env['CLAUDE_CONFIG_DIR']) / 'settings.json', {'enabledPlugins': {'w0208-test@local': False}})
        write(cwd / '.claude/settings.json', '{bad fixture json' if mode == 'malformed' else {'env': {'NETCAP_RESOLVER_FIXTURE': 'project'}})
        marker = case / 'helper-ran'
        helper = case / 'policy-helper'
        write(helper, '#!/usr/bin/python3\nfrom pathlib import Path\nPath(' + repr(str(marker)) + ').write_text("ran")\nprint("{}")\n')
        helper.chmod(0o700)
        write(case / 'request.json', {'root': str(case), 'cwd': str(cwd), 'mode': mode,
                                      'helper': str(helper), 'helperMarker': str(marker)})
        proc = subprocess.Popen(['node', str(Path(__file__).with_suffix('.mjs')), str(case / 'request.json')],
                                cwd=cwd, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                text=True, start_new_session=True)
        try:
            stdout, stderr = proc.communicate(timeout=20)
            write(case / 'stdout.log', stdout)
            write(case / 'stderr.log', stderr)
        finally:
            stop(proc)
        result = json.loads((case / 'result.json').read_text()) if (case / 'result.json').exists() else {}
        result['runner_exit_code'] = proc.returncode
        results[mode] = result
    checks = {'runners_completed': all(x['runner_exit_code'] == 0 for x in results.values())}
    if args.host:
        checks['host_provenance_reads_available'] = (
            not results['host'].get('provenance_requires_reconciliation', True)
            and {r.get('hive') for r in results['host'].get('reads', [])} == {'hklm', 'hkcu'}
            and all(r.get('outcome') in {'present', 'absent'} for r in results['host']['reads'])
        )
    else:
        checks.update({
            'absent_returns_fixture_sources': results['absent'].get('resolver_returned') and len(results['absent'].get('sources', [])) == 2,
            'denied_is_not_reported_as_resolver_error': results['denied'].get('resolver_returned') and not results['denied'].get('has_errors_field'),
            'unavailable_is_not_reported_as_resolver_error': results['unavailable'].get('resolver_returned') and not results['unavailable'].get('has_errors_field'),
            'denied_read_must_remain_unknown': results['denied'].get('provenance_requires_reconciliation'),
            'unavailable_read_must_remain_unknown': results['unavailable'].get('provenance_requires_reconciliation'),
            'policy_helper_not_executed': results['helper'].get('policy_helper_reported') and not results['helper'].get('policy_helper_executed'),
            'policy_helper_must_remain_unknown': results['helper'].get('provenance_requires_reconciliation'),
            'malformed_source_is_omitted': results['malformed'].get('resolver_returned') and all(s['source'] != 'project' for s in results['malformed'].get('sources', [])),
            'malformed_source_must_remain_unknown': results['malformed'].get('provenance_requires_reconciliation'),
        })
    report = {'checks': checks, 'cases': results, 'passed': all(checks.values()),
              'production_preflight_verified': False,
              'scope': 'SDK metadata contract and missing-evidence diagnosis only; no source admission or policy enforcement claim'}
    write(root / 'result.json', report)
    print(json.dumps({'checks': checks, 'passed': report['passed'], 'production_preflight_verified': False}, indent=2))
    raise SystemExit(0 if report['passed'] else 1)


if __name__ == '__main__':
    main()
