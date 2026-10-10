#!/usr/bin/env python3
"""Real Skill/body/relative-file protocol probe with a local scripted provider.

The provider follows a fixed tool sequence. This proves delivery, not a real
model's autonomous method adoption or the W02.07 method Gate.
"""
import argparse
import json
from pathlib import Path
import re
import subprocess
import tempfile
import uuid
from execution_config_probe import Provider, environment, verify_versions, write
from runtime.common import stop


def strings(value):
    if isinstance(value, str):
        return value
    if isinstance(value, list):
        return '\n'.join(strings(x) for x in value)
    if isinstance(value, dict):
        return '\n'.join(strings(x) for x in value.values())
    return ''


class SkillProvider(Provider):
    def plan(self, payload):
        messages = payload.get('messages', [])
        marked = [(i, re.search(r'W0207_METHOD_CASE=(enabled|disabled|restored)', strings(m.get('content')))) for i, m in enumerate(messages) if m.get('role') == 'user']
        marked = [(i, match.group(1)) for i, match in marked if match]
        if not marked:
            return {'text': 'No diagnostic task.'}
        start, case = marked[-1]
        current = strings(messages[start:])
        if case == 'restored':
            return {'text': 'W0207_RESTORED_WITH_HISTORY'}
        tool_issued = any(m.get('role') == 'assistant' and any(b.get('type') == 'tool_use' for b in m.get('content', []) if isinstance(b, dict)) for m in messages[start:])
        if not tool_issued:
            return {'tool': 'Skill', 'input': {'skill': self.skill}}
        body = re.search(r'W0207_BODY_TOKEN=([a-f0-9]+)', current)
        data = re.search(r'W0207_DATA_TOKEN=([a-f0-9]+)', current)
        if body and data:
            return {'text': f'W0207_PROTOCOL_RESULT={body.group(1)}/{data.group(1)}'}
        if body:
            if any(m.get('role') == 'assistant' and any(b.get('type') == 'tool_use' and b.get('name') == 'Read' for b in m.get('content', []) if isinstance(b, dict)) for m in messages[start:]):
                return {'text': 'W0207_READ_UNAVAILABLE'}
            base = re.search(r'Base directory for this skill:\s*([^\n]+)', current)
            if not base:
                return {'text': 'W0207_MISSING_NATIVE_BASE_DIRECTORY'}
            return {'tool': 'Read', 'input': {'file_path': str(Path(base.group(1).strip()) / 'data.txt')}}
        return {'text': 'W0207_SKILL_UNAVAILABLE'}

    def anthropic(self, payload, answer, seq):
        action = self.plan(payload)
        if 'text' in action:
            return super().anthropic(payload, action['text'], seq)
        message = {'id': f'msg_{seq}', 'type': 'message', 'role': 'assistant', 'model': payload['model'], 'content': [], 'stop_reason': None, 'stop_sequence': None, 'usage': {'input_tokens': 32, 'output_tokens': 0}}
        return self.sse([
            ('message_start', {'message': message}),
            ('content_block_start', {'index': 0, 'content_block': {'type': 'tool_use', 'id': f'tool_{seq}', 'name': action['tool'], 'input': {}}}),
            ('content_block_delta', {'index': 0, 'delta': {'type': 'input_json_delta', 'partial_json': json.dumps(action['input'])}}),
            ('content_block_stop', {'index': 0}),
            ('message_delta', {'delta': {'stop_reason': 'tool_use', 'stop_sequence': None}, 'usage': {'output_tokens': 32}}),
            ('message_stop', {}),
        ])


def planner_checks():
    provider = SkillProvider.__new__(SkillProvider)
    provider.skill = 'package:method'
    initial = [{'role': 'user', 'content': 'W0207_METHOD_CASE=enabled'}]
    invoked = initial + [{'role': 'assistant', 'content': [{'type': 'tool_use', 'name': 'Skill'}]}]
    body = invoked + [{'role': 'user', 'content': 'W0207_BODY_TOKEN=abc\nBase directory for this skill: /fixture/method'}]
    complete = body + [{'role': 'user', 'content': [{'type': 'tool_result', 'content': 'W0207_DATA_TOKEN=def'}]}]
    checks = {
        'requests_canonical_skill': provider.plan({'messages': initial}) == {'tool': 'Skill', 'input': {'skill': 'package:method'}},
        'does_not_invent_body': provider.plan({'messages': invoked}) == {'text': 'W0207_SKILL_UNAVAILABLE'},
        'relative_read_uses_native_base': provider.plan({'messages': body}) == {'tool': 'Read', 'input': {'file_path': '/fixture/method/data.txt'}},
        'result_uses_observed_tokens': provider.plan({'messages': complete}) == {'text': 'W0207_PROTOCOL_RESULT=abc/def'},
        'historical_body_not_new_delivery': provider.plan({'messages': complete + [{'role': 'user', 'content': 'W0207_METHOD_CASE=disabled'}]}) == {'tool': 'Skill', 'input': {'skill': 'package:method'}},
    }
    assert all(checks.values()), checks
    return checks


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--planner-only', action='store_true')
    args = parser.parse_args()
    if args.planner_only:
        print(json.dumps({'checks': planner_checks(), 'protocol_verified': False, 'scope': 'scripted provider planner only; no native CLI, listener or model input'}, indent=2))
        return
    root = Path(tempfile.mkdtemp(prefix='netcap-w0207-method-'))
    print('Evidence directory:', root, flush=True)
    verify_versions(root, ['claude'])
    body_token, data_token = uuid.uuid4().hex, uuid.uuid4().hex
    source = root / 'method'
    write(source / 'SKILL.md', f'---\nname: method\ndescription: W0207_METHOD_DESCRIPTION\n---\nW0207_BODY_TOKEN={body_token}\nRead data.txt relative to this method directory. Report the body and data markers exactly.\n')
    write(source / 'data.txt', f'W0207_DATA_TOKEN={data_token}\n')
    cwd = root / 'work'
    cwd.mkdir()
    provider = SkillProvider(root, 'A')
    provider.skill = 'netcap-skill-' + 'a' * 24 + ':method'
    write(root / 'request.json', {'root': str(root), 'cwd': str(cwd), 'source': str(source), 'endpoint': provider.url})
    proc = None
    try:
        proc = subprocess.Popen(['node', str(Path(__file__).with_suffix('.mjs')), str(root / 'request.json')], env=environment(root), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
        stdout, stderr = proc.communicate(timeout=180)
        write(root / 'stdout.log', stdout)
        write(root / 'stderr.log', stderr)
        result = json.loads((root / 'result.json').read_text()) if (root / 'result.json').exists() else {'checks': {}}
        requests = [r for r in provider.requests if 'count_tokens' not in r['path']]
        enabled = [r for r in requests if 'W0207_METHOD_CASE=enabled' in strings(r['body'].get('messages'))]
        disabled = [r for r in requests if 'W0207_METHOD_CASE=disabled' in strings(r['body'].get('messages'))]
        checks = result['checks']
        checks.update({
            'native_exit': proc.returncode == 0,
            'initial_description_without_body': bool(enabled) and 'W0207_METHOD_DESCRIPTION' in strings(enabled[0]['body']) and body_token not in strings(enabled[0]['body']),
            'body_reaches_provider_after_skill': any(body_token in strings(r['body'].get('messages')) for r in enabled[1:]),
            'data_reaches_provider_after_read': any(data_token in strings(r['body'].get('messages')) for r in enabled[1:]),
            'disabled_has_no_body_or_data': bool(disabled) and all(body_token not in strings(r['body']) and data_token not in strings(r['body']) for r in disabled),
            'local_auth_and_provider_clean': bool(requests) and all(r['auth_matches'] for r in requests) and not provider.errors,
        })
        result.update(protocol_verified=all(checks.values()),method_gate_verified=False,scope='real CLI with a fixed scripted local provider; body/Read delivery and historical configuration, not autonomous method use')
        write(root / 'result.json', result)
        print(json.dumps({'checks': checks, 'protocol_verified': result['protocol_verified'], 'method_gate_verified': False}, indent=2))
        raise SystemExit(0 if result['protocol_verified'] else 1)
    finally:
        if proc:
            stop(proc)
        provider.close()


if __name__ == '__main__':
    main()
