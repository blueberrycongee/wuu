#!/usr/bin/env python3
"""Verify crash and cancellation accounting with synthetic evaluation processes."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys


FIXTURE = '''#!/usr/bin/env python3
import json, os, signal, sys, time
from pathlib import Path
directory = Path(sys.argv[sys.argv.index('--artifacts') + 1])
task = directory / 'first'
task.mkdir(parents=True)
(task / 'result.json').write_text(json.dumps({'task_id': 'first', 'success': True, 'input_tokens': 321}))
if os.environ['WUU_RECOVERY_CASE'] == 'cancel':
    os.kill(os.getppid(), signal.SIGTERM)
    time.sleep(60)
raise SystemExit(7)
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    host = output / 'fixture'
    host.write_text(FIXTURE)
    host.chmod(0o700)
    plan = output / 'plan.json'
    plan.write_text(json.dumps({'version': 1, 'tasks': ['first', 'second'], 'concurrency': 1}))
    results = []
    for mode in ['crash', 'cancel']:
        pair = output / mode
        process = subprocess.run([sys.executable, str(Path(__file__).with_name('run-pair.py')),
            '--plan', str(plan), '--baseline', str(host), '--source-baseline', str(host),
            '--output', str(pair), '--timeout', '5'], capture_output=True, text=True,
            env={**os.environ, 'WUU_RECOVERY_CASE': mode}, timeout=15)
        record = json.loads((pair / 'pair.json').read_text())
        baseline = record['attempts'][0]
        first, second = baseline['tasks']
        passed = (process.returncode != 0 and record['decision'] == 'reject'
                  and record['denominator'] == 2 and len(record['attempts']) == 2
                  and first['recorded'] and first['passed'] and not second['passed']
                  and first.get('result', {}).get('input_tokens') == 321
                  and baseline.get('process', {}).get('exit_code') is not None
                  and not baseline['complete_report'])
        results.append({'case': mode, 'passed': passed, 'pair': str(pair / 'pair.json')})
    (output / 'result.json').write_text(json.dumps(results, indent=2) + '\n')
    print(json.dumps(results, indent=2))
    return 0 if all(item['passed'] for item in results) else 1


if __name__ == '__main__':
    raise SystemExit(main())
