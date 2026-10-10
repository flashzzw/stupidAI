#!/usr/bin/env python3
"""Diagnose native hook completion using only an owned app-server and its children.

The command appends its PID to an isolated marker file and exits successfully.
The existing probe still requires a successful native completion: observing a
zero exit status is diagnostic evidence, never a substitute for that result.
No listeners, real model providers, user profiles or shared daemons are used.
"""
import json
import os
from pathlib import Path
import shlex
import tempfile
import threading
import time

from codex_hook_execution_probe import run
from codex_skill_selection_probe import Stdio
from execution_config_probe import write


def process_sample(pid):
    base = Path('/proc') / str(pid)
    status = dict(line.split(':', 1) for line in (base / 'status').read_text().splitlines()
                  if ':' in line)
    # The comm field can contain spaces and parentheses. Fields after its last
    # closing parenthesis begin at the documented state field (field 3).
    fields = (base / 'stat').read_text().rsplit(') ', 1)[1].split()
    sample = {'pid': pid, 'ppid': int(status['PPid']), 'name': status['Name'].strip(),
              'state': status['State'].strip(), 'start_ticks': int(fields[19]),
              'blocked_signals': status['SigBlk'].strip(),
              'pending_signals': status['ShdPnd'].strip()}
    if fields[0] == 'Z' and len(fields) > 49:
        sample['exit_status'] = int(fields[49])
        sample['exit_code'] = os.waitstatus_to_exitcode(sample['exit_status'])
    return sample


class ObservedStdio(Stdio):
    def __init__(self, root, env, label):
        env = dict(env, RUST_LOG='error,codex_hooks::engine::command_runner=trace')
        super().__init__(root, env, label)
        self.samples = []
        self.observation_errors = set()
        self.observation_stop = threading.Event()
        self.observer = threading.Thread(target=self.observe, daemon=True)
        self.observer.start()

    def observe(self):
        start = time.monotonic()
        previous = None
        while not self.observation_stop.is_set():
            pending, seen, processes = [self.proc.pid], set(), []
            while pending:
                pid = pending.pop()
                if pid in seen:
                    continue
                seen.add(pid)
                try:
                    processes.append(process_sample(pid))
                    # A child may belong to any app-server worker thread.
                    for task in (Path('/proc') / str(pid) / 'task').iterdir():
                        pending.extend(int(v) for v in (task / 'children').read_text().split())
                except (FileNotFoundError, ProcessLookupError):
                    pass  # Process exit can race a read; absence is not success.
                except OSError as error:
                    self.observation_errors.add(type(error).__name__)
            processes.sort(key=lambda p: p['pid'])
            if processes != previous:
                self.samples.append({'elapsed_ms': round((time.monotonic()-start)*1000),
                                     'processes': processes})
                previous = processes
            self.observation_stop.wait(.02)

    def close(self):
        if hasattr(self, 'observation_stop'):
            self.observation_stop.set()
            self.observer.join(timeout=2)
            write(self.root / 'owned-processes.json', {'app_server_pid': self.proc.pid,
                'samples': self.samples, 'errors': sorted(self.observation_errors),
                'scope': 'owned descendants; /proc status/stat only; no environment or fd access'})
        super().close()


def main():
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-hook-lifecycle-'))
    print('Evidence directory:', root, flush=True)
    marker = root / 'command-pids.txt'
    command = "printf '%s\\n' \"$$\" >> " + shlex.quote(str(marker))
    passed = False
    try:
        passed = run(root, 3, command, client_factory=ObservedStdio)
    finally:
        report_path, processes_path = root / 'result.json', root / 'owned-processes.json'
        if report_path.exists() and processes_path.exists():
            result = json.loads(report_path.read_text())
            observations = json.loads(processes_path.read_text())
            pids = [int(v) for v in marker.read_text().split()] if marker.exists() else []
            native_pid = pids[1] if len(pids) == 2 else None
            states = [{'elapsed_ms': sample['elapsed_ms'], **p}
                for sample in observations['samples'] for p in sample['processes']
                if p['pid'] == native_pid and p['ppid'] == observations['app_server_pid']]
            protocol = json.loads((root / 'native-protocol.json').read_text())
            diagnosis = {'root': str(root), 'command_pids': pids, 'native_command_pid': native_pid,
                'native_command_states': states,
                'native_command_observed_exit_0': any(p.get('exit_code') == 0 for p in states),
                'native_completion_passed': result['checks'].get('trusted_command_completes', False),
                'server_requests': [{'id': e['id'], 'method': e['method']}
                    for e in protocol['events'] if 'id' in e and 'method' in e],
                'observation_errors': observations['errors'], 'expectations_met': passed,
                'scope': 'PID/exit observation diagnoses lifecycle only; native completion remains authoritative'}
            write(root / 'diagnosis.json', diagnosis)
            print(json.dumps({k: diagnosis[k] for k in ['native_command_observed_exit_0',
                'native_completion_passed', 'server_requests', 'expectations_met']}, indent=2))
    raise SystemExit(0 if passed else 1)


if __name__ == '__main__':
    main()
