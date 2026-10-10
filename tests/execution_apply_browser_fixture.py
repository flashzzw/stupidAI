#!/usr/bin/env python3
"""Owned browser fixture; the sentinel restarts only this fixture's native handle."""
import argparse
import json
import os
from pathlib import Path
import signal
import time

from native_mcp_selection_smoke import fixture_for


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--netcap',default='.dev/bin/netcap')
    parser.add_argument('--info',required=True)
    args=parser.parse_args()
    stopped=False
    def finish(*_):
        nonlocal stopped
        stopped=True
    signal.signal(signal.SIGTERM,finish)
    signal.signal(signal.SIGINT,finish)
    with fixture_for(str(Path(args.netcap).resolve())) as f:
        info=dict(f.info,root=str(f.root),harness_pid=os.getpid())
        Path(args.info).write_text(json.dumps(info))
        print('READY',f.info['url'],flush=True)
        while not stopped:
            assert f.console.poll() is None
            request=f.root/'restart-native.request'
            if request.exists():
                request.unlink()
                f.restart_native()
                (f.root/'restart-native.done').write_text('completed')
            time.sleep(.1)


if __name__=='__main__': main()
