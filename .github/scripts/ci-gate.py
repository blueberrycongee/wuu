import json
import os
import sys


def require(result, selected, name):
    wanted = 'success' if selected else 'skipped'
    if result != wanted:
        raise SystemExit(f'{name}: expected {wanted}, got {result}')


def go_gate(env):
    require(env['SELECTION_RESULT'], True, 'changes')
    for name, flag in [('GO_VALIDATION_RESULT', 'GO_REQUIRED'),
                       ('GO_RACE_RESULT', 'GO_REQUIRED'),
                       ('ARCHITECTURE_RESULT', 'ARCHITECTURE_REQUIRED')]:
        if env[flag] not in ('true', 'false'):
            raise SystemExit(f'Invalid selection: {flag}')
        require(env[name], env[flag] == 'true', name)


def desktop_gate(env):
    require(env['SELECTION_RESULT'], True, 'changes')
    suites = json.loads(env['DESKTOP_SUITES'])
    e2e = json.loads(env['E2E_SUITES'])
    flag = env['DESKTOP_REQUIRED']
    if not isinstance(suites, list) or flag not in ('true', 'false') or bool(suites) != (flag == 'true'):
        raise SystemExit('Invalid Desktop selection')
    unit = [suite for suite in suites if suite.startswith('unit')]
    if unit not in ([], ['unit', 'unit-extra']) or e2e != [suite for suite in suites if not suite.startswith('unit')]:
        raise SystemExit('Desktop partition differs from complete selection')
    for name, selected in [('UNIT_RESULT', unit), ('SUITES_RESULT', e2e)]:
        require(env[name], bool(selected), name)


def ci_gate(env):
    needs = json.loads(env['RESULTS'])
    selected = needs['changes']['outputs']
    go_gate({
        'SELECTION_RESULT': needs['changes']['result'],
        'GO_REQUIRED': selected['go'],
        'ARCHITECTURE_REQUIRED': selected['architecture'],
        'GO_VALIDATION_RESULT': needs['go-validation']['result'],
        'GO_RACE_RESULT': needs['go-race']['result'],
        'ARCHITECTURE_RESULT': needs['architecture-conformance']['result'],
    })
    desktop_gate({
        'SELECTION_RESULT': needs['changes']['result'],
        'DESKTOP_REQUIRED': selected['desktop'],
        'DESKTOP_SUITES': selected['desktop_suites'],
        'E2E_SUITES': selected['desktop_e2e_suites'],
        'UNIT_RESULT': needs['desktop-unit']['result'],
        'SUITES_RESULT': needs['desktop-suite']['result'],
    })
    expected = {
        'repository-check': True,
        'desktop-macos-controls': selected['desktop'] == 'true' and 'shell' in json.loads(selected['desktop_suites']),
        'clients-check': selected['clients'] == 'true',
        'macos-check': selected['native'] == 'true',
        'windows-check': selected['native'] == 'true',
        'linux-check': selected['native'] == 'true',
    }
    for job, required in expected.items():
        require(needs[job]['result'], required, job)


if __name__ == '__main__':
    {'go': go_gate, 'desktop': desktop_gate, 'ci': ci_gate}[sys.argv[1]](os.environ)
