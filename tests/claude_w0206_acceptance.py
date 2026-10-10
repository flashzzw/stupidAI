#!/usr/bin/env python3
"""Run the two outstanding W02.06 native regressions with local fixtures.

Uses the existing development build and stops on the first failed or missing
result. No real providers, user profile changes, or shared daemon operations.
Passing these cases does not mark the W series complete.
"""
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile

REPO = Path(__file__).resolve().parents[1]
CASES = (
    ('stdio', 'claude_mcp_selection_smoke.py'),
    ('resources', 'claude_execution_resources_smoke.py'),
)


def main():
    root = Path(tempfile.mkdtemp(prefix='netcap-w0206-acceptance-'))
    print('Acceptance directory:', root, flush=True)
    binary = REPO / '.dev/bin/netcap'
    report = {
        'scope': 'stdio selection and mixed-resource same-ID changes only; local providers',
        'cases': {}, 'expectations_met': False,
    }
    try:
        report['binary_sha256'] = hashlib.sha256(binary.read_bytes()).hexdigest()
        for name, script in CASES:
            print('Running:', name, flush=True)
            # Run synchronously; each existing fixture owns and closes its
            # subprocesses. Do not kill a harness and strand its child services.
            with (root / (name + '.txt')).open('w') as log:
                completed = subprocess.run(
                    [sys.executable, str(REPO / 'tests' / script)], cwd=REPO,
                    stdout=log, stderr=subprocess.STDOUT, check=False)
            output = (root / (name + '.txt')).read_text()
            directories = re.findall(r'^Evidence directory: (.+)$', output, re.MULTILINE)
            item = {'exit_code': completed.returncode, 'source_script': script}
            report['cases'][name] = item
            if len(directories) != 1:
                raise RuntimeError(name + ': missing or ambiguous evidence directory; inspect case log')
            directory = Path(directories[0])
            item['directory'] = str(directory)
            result = json.loads((directory / 'result.json').read_text())
            checks = result.get('checks')
            item['checks'] = checks
            item['expectations_met'] = (
                completed.returncode == 0 and result.get('expectations_met') is True
                and isinstance(checks, dict) and bool(checks)
                and all(value is True for value in checks.values()))
            if not item['expectations_met']:
                raise RuntimeError(name + ': native acceptance failed; inspect original result')
            print(name + ': passed; ' + str(directory), flush=True)
        report['expectations_met'] = True
    except (OSError, ValueError, RuntimeError) as error:
        report['error'] = str(error)
    finally:
        (root / 'result.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(report, ensure_ascii=False, indent=2), flush=True)
    return 0 if report['expectations_met'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
