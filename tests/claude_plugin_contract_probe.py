#!/usr/bin/env python3
"""Pinned Claude plugin protocol experiment; no input, model or MCP connection.

Uses owned CLI stdio directly because SDK 0.3.280 declares get_hooks_listing in
the wire schema but does not expose it on Query. Reloads are deliberate writes
to these isolated queries, never a candidate implementation of a read API.
"""
import argparse
import hashlib
import json
from pathlib import Path
import queue
import subprocess
import tempfile
import threading
import time
import uuid

from execution_config_probe import CLAUDE, environment, verify_versions, write
from runtime.common import stop

MODES = ['enabled', 'omitted', 'flag-disabled', 'name-disabled', 'hooks-disabled',
         'skill-disabled', 'skip-mcp', 'late-added', 'live-disabled', 'updated',
         'same-name', 'same-name-disabled', 'invalid-manifest', 'installed-omitted',
         'installed-disabled', 'installed-wrong-source-disabled', 'installed-and-inline',
         'installed-source-update', 'installed-inline-disabled', 'installed-source-disabled',
         'installed-duplicate']


class Native:
    def __init__(self, case, env, cwd, settings=None, extra=()):
        self.case = case
        self.messages = queue.Queue()
        self.outgoing = []
        self.incoming = []
        self.read_error = ''
        self.err = (case / 'stderr.log').open('w')
        self.id = str(uuid.uuid4())
        self.proc = subprocess.Popen([
            CLAUDE, '-p', '--input-format', 'stream-json', '--output-format',
            'stream-json', '--verbose', '--await-initialize', '--session-id', self.id,
            '--setting-sources', 'user,project,local', '--model', 'claude-sonnet-4-6',
            '--settings', settings if isinstance(settings, str) else json.dumps(settings or {}), *extra,
        ], cwd=cwd, env={**env, 'ANTHROPIC_API_KEY': 'fixture-no-model',
                        'ANTHROPIC_BASE_URL': 'http://127.0.0.1:1'},
            text=True, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=self.err, start_new_session=True)
        self.reader = threading.Thread(target=self.read, daemon=True)
        self.reader.start()

    def read(self):
        try:
            for line in self.proc.stdout:
                message = json.loads(line)
                self.incoming.append(message)
                self.messages.put(message)
        except Exception as error:
            self.read_error = str(error)
        finally:
            self.messages.put(None)

    def call(self, subtype, **params):
        rid = str(uuid.uuid4())
        request = {'type': 'control_request', 'request_id': rid,
                   'request': {'subtype': subtype, **params}}
        self.outgoing.append(request)
        self.proc.stdin.write(json.dumps(request) + '\n')
        self.proc.stdin.flush()
        deadline = time.monotonic() + 30
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError('native control response deadline exceeded')
            message = self.messages.get(timeout=remaining)
            if message is None:
                raise RuntimeError('native CLI exited before control response')
            if message.get('type') == 'control_request':
                raise RuntimeError('unexpected native request in no-input experiment')
            response = message.get('response', {})
            if message.get('type') == 'control_response' and response.get('request_id') == rid:
                if response.get('subtype') != 'success':
                    raise RuntimeError(str(response))
                return response.get('response', {})

    def close(self):
        stop(self.proc)
        self.reader.join(timeout=5)
        self.proc.stdin.close()
        self.proc.stdout.close()
        self.err.close()
        write(self.case / 'wire.json', {'outgoing': self.outgoing, 'incoming': self.incoming,
                                      'read_error': self.read_error, 'exit_code': self.proc.returncode})


def plugin(root, name, version='1.0.0'):
    write(root / '.claude-plugin/plugin.json', {'name': name, 'version': version})
    suffix = '-v2' if version == '2.0.0' else ''
    write(root / 'skills/method/SKILL.md', f'---\nname: {name}-method{suffix}\ndescription: {name} method {version}\n---\nPRIVATE_METHOD_BODY\n')
    write(root / 'commands/action.md', f'---\ndescription: {name} command {version}\n---\nPRIVATE_COMMAND_BODY\n')
    write(root / 'agents/check.md', f'---\nname: {name}-checker\ndescription: {name} agent\n---\nPRIVATE_AGENT_BODY\n')
    # Never fires in a zero-input query; no network or process is needed for
    # source/disabled registration checks. Execution is a separate gate.
    write(root / 'hooks/hooks.json', {'hooks': {'PreToolUse': [{
        'matcher': 'Read', 'hooks': [{'type': 'command', 'command': 'exit 8' if suffix else 'exit 7'}]}]}})


def sample(native):
    return {'context': native.call('get_context_usage', detail='summary'),
            'hooks': native.call('get_hooks_listing'),
            'mcp': native.call('mcp_status')}


def digest_tree(root):
    return {str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest()
            for p in sorted(root.rglob('*')) if p.is_file()}


def components(result, stage='before'):
    listing = result['initialized' if stage == 'before' else 'reloaded']
    state = result[stage]
    return {'commands': [c for c in listing['commands'] if c['name'].startswith('w0208')],
            'agents': [a for a in listing['agents'] if a['name'].startswith('w0208')],
            'descriptions': [s for s in state['context']['skills']['skillFrontmatter'] if s['name'].startswith('w0208')],
            'hooks': state['hooks']['hooks'], 'hook_policy': state['hooks']['policy'],
            'mcp': state['mcp']['mcpServers']}


def selected_components(view):
    return {key: [x for x in view[key] if x.get('name', x.get('pluginName', '')).startswith('w0208-selected')]
            for key in ['commands', 'agents', 'descriptions', 'hooks']}


def component_counts(view, prefix):
    return [len([x for x in view[key] if x.get('name', x.get('pluginName', '')).startswith(prefix)])
            for key in ['commands', 'agents', 'descriptions', 'hooks']]


def install(case, env, cwd):
    marketplace = case / 'marketplace'
    write(marketplace / '.claude-plugin/marketplace.json', {'name': 'w0208-local',
        'owner': {'name': 'Owned fixture'},
        'plugins': [{'name': 'w0208-selected', 'source': './selected'}]})
    outputs = []
    for args in [['plugin', 'marketplace', 'add', str(marketplace)],
                 ['plugin', 'install', 'w0208-selected@w0208-local', '--scope', 'user', '--json']]:
        process = subprocess.Popen([CLAUDE, *args], cwd=cwd, env=env, text=True,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
        try:
            stdout, stderr = process.communicate(timeout=30)
            outputs.append({'args': args, 'exit_code': process.returncode, 'stdout': stdout, 'stderr': stderr})
        finally:
            stop(process)
            write(case / 'installation.json', outputs)
        if process.returncode != 0:
            raise RuntimeError('owned local plugin installation failed')


def run(root, modes):
    verify_versions(root, ['claude'])
    reports = {}
    for mode in modes:
        case = root / mode
        env = environment(case)
        cwd = case / 'work'
        cwd.mkdir()
        selected, other = case / 'marketplace/selected', case / 'other'
        plugin(selected, 'w0208-selected')
        plugin(other, 'w0208-other')
        write(cwd / '.claude/skills/project/SKILL.md', '---\nname: w0208-project\ndescription: preserved project method\n---\nPROJECT_BODY\n')
        settings = {}
        plugins = [{'type': 'local', 'path': str(other)}, {'type': 'local', 'path': str(selected)}]
        if mode == 'omitted':
            plugins.pop()
        if mode == 'flag-disabled':
            settings['enabledPlugins'] = {'w0208-selected@inline': False}
        if mode == 'hooks-disabled':
            settings['disableAllHooks'] = True
        if mode == 'name-disabled':
            settings['enabledPlugins'] = {'w0208-selected': False}
        if mode == 'skill-disabled':
            settings['skillOverrides'] = {'w0208-selected:method': 'off',
                                          'w0208-selected:w0208-selected-method': 'off'}
        if mode == 'skip-mcp':
            # If suppression fails these commands record discovery and exit.
            # There is intentionally no functional MCP server or TCP endpoint.
            sentinel = case / 'mcp-discovery.txt'
            marker = case / 'mcp_marker.py'
            write(marker, f'from pathlib import Path\nPath({str(sentinel)!r}).write_text("UNEXPECTED_MCP_DISCOVERY")\n')
            definition = {'command': '/usr/bin/python3', 'args': [str(marker)]}
            write(selected / '.mcp.json', {'mcpServers': {'file-server': definition}})
            write(selected / '.claude-plugin/plugin.json', {'name': 'w0208-selected',
                'version': '1.0.0', 'mcpServers': {'inline-server': definition}})
            plugins[-1]['skipMcpDiscovery'] = True
        if mode == 'late-added':
            plugins.pop()
        if mode in ['same-name', 'same-name-disabled']:
            plugin(case / 'duplicate', 'w0208-selected', '9.0.0')
            plugins.append({'type': 'local', 'path': str(case / 'duplicate')})
            if mode == 'same-name-disabled':
                settings['enabledPlugins'] = {'w0208-selected@inline': False}
        if mode == 'invalid-manifest':
            write(selected / '.claude-plugin/plugin.json', '{invalid json')
        if mode.startswith('installed-'):
            install(case, env, cwd)
            if mode not in ['installed-and-inline', 'installed-inline-disabled', 'installed-source-disabled', 'installed-duplicate']:
                plugins.pop()
            if mode == 'installed-disabled':
                settings['enabledPlugins'] = {'w0208-selected@w0208-local': False}
            if mode == 'installed-wrong-source-disabled':
                settings['enabledPlugins'] = {'w0208-selected@inline': False}
            if mode == 'installed-inline-disabled':
                settings['enabledPlugins'] = {'w0208-selected@inline': False}
            if mode == 'installed-source-disabled':
                settings['enabledPlugins'] = {'w0208-selected@w0208-local': False}
            if mode == 'installed-duplicate':
                plugin(case / 'duplicate', 'w0208-selected', '9.0.0')
                plugins[-1]['path'] = str(case / 'duplicate')
        resources_before = {p.name: digest_tree(p) for p in [selected, other, cwd]}
        write(case / 'request.json', {'plugins': plugins, 'settings': settings, 'cwd': str(cwd),
                                    'resources_before': resources_before})
        native = Native(case, env, cwd, settings)
        result = {'native_id': native.id}
        try:
            result['initialized'] = native.call('initialize', plugins=plugins)
            result['before'] = sample(native)
            if mode == 'late-added':
                result['reinitialized'] = native.call('initialize', plugins=plugins + [{'type': 'local', 'path': str(selected)}])
            if mode == 'live-disabled':
                result['flag_change'] = native.call('apply_flag_settings', settings={'enabledPlugins': {'w0208-selected@inline': False}})
                result['flag_before_reload'] = sample(native)
            if mode in ['updated', 'installed-source-update']:
                plugin(selected, 'w0208-selected', '2.0.0')
                result['disk_changed_before_reload'] = sample(native)
            result['reloaded'] = native.call('reload_plugins')
            result['after'] = sample(native)
        except Exception as error:
            result['error'] = str(error)
        finally:
            native.close()
        result['resources_after'] = {p.name: digest_tree(p) for p in [selected, other, cwd]}
        result['checks'] = {
            'native_responses_complete': not result.get('error') and not native.read_error,
            'only_control_requests': all(m['type'] == 'control_request' for m in native.outgoing),
            'no_model_results': not any(m.get('type') == 'result' for m in native.incoming),
            'no_tool_or_permission_execution': not any(m.get('type') in ['assistant', 'user', 'control_request'] or m.get('subtype') in ['hook_started', 'hook_response'] for m in native.incoming),
            'owned_cli_stopped': native.proc.poll() is not None and not native.reader.is_alive(),
            'unmodified_sources_preserved': all(result['resources_after'][key] == value
                for key, value in resources_before.items()
                if key != 'selected' or mode not in ['updated', 'installed-source-update']),
        }
        if not result.get('error'):
            result['observations'] = {'initial_plugins_applied': result['initialized'].get('plugins_applied'),
                                      'before': components(result), 'after': components(result, 'after'),
                                      'plugins_after_reload': result['reloaded']['plugins'],
                                      'reload_error_count': result['reloaded']['error_count']}
            before, after = [result['observations'][step] for step in ['before', 'after']]
            current = selected_components(after)
            hidden = mode in ['omitted', 'flag-disabled', 'late-added', 'same-name-disabled', 'invalid-manifest', 'installed-disabled']
            result['checks']['initial_component_visibility'] = component_counts(before, 'w0208-selected') == ([0, 0, 0, 0] if hidden else [2, 1, 2, 1])
            after_hidden = hidden or mode == 'live-disabled'
            result['checks']['reloaded_component_visibility'] = component_counts(after, 'w0208-selected') == ([0, 0, 0, 0] if after_hidden else [2, 1, 2, 1])
            result['checks']['other_plugin_components_present'] = component_counts(before, 'w0208-other') == component_counts(after, 'w0208-other') == [2, 1, 2, 1]
            result['checks']['other_components_preserved'] = all(
                [x for x in before[key] if x.get('name', x.get('pluginName', '')).startswith('w0208-other')] ==
                [x for x in after[key] if x.get('name', x.get('pluginName', '')).startswith('w0208-other')]
                for key in ['commands', 'agents', 'descriptions', 'hooks'])
            result['checks']['project_method_preserved'] = all(any(c['name'] == 'w0208-project' for c in s['descriptions']) for s in [before, after])
            result['checks']['no_mcp_connection'] = before['mcp'] == after['mcp'] == []
            result['checks']['reload_errors_reported'] = result['reloaded']['error_count'] == (1 if mode == 'invalid-manifest' else 0)
            result['checks']['no_body_in_inventory'] = not any(marker in json.dumps(result) for marker in ['PRIVATE_METHOD_BODY', 'PRIVATE_COMMAND_BODY', 'PRIVATE_AGENT_BODY'])
            if mode in ['flag-disabled', 'invalid-manifest']:
                result['checks']['applied_flag_does_not_prove_components_enabled'] = result['initialized'].get('plugins_applied') is True and all(not rows for rows in current.values())
            if mode == 'hooks-disabled':
                result['checks']['global_hook_disable_affects_other_plugin'] = all(h.get('disabled') is True for h in before['hooks'] + after['hooks']) and len(after['hooks']) == 2
            if mode == 'late-added':
                result['checks']['reinitialize_cannot_add_plugin'] = result['reinitialized'].get('plugins_applied') is False and all(not rows for rows in current.values())
            if mode == 'live-disabled':
                unchanged = result['flag_before_reload']
                result['checks']['flag_write_alone_leaves_cached_components'] = unchanged['context']['skills'] == result['before']['context']['skills'] and unchanged['hooks'] == result['before']['hooks']
            if mode in ['updated', 'installed-source-update']:
                changed = result['disk_changed_before_reload']
                result['checks']['read_only_sample_keeps_previous_components'] = changed['context']['skills'] == result['before']['context']['skills'] and changed['hooks'] == result['before']['hooks']
                result['checks']['fixture_source_changed'] = resources_before['selected'] != result['resources_after']['selected']
                result['checks']['reload_reads_mutable_local_source'] = [p['version'] for p in result['reloaded']['plugins'] if p['name'] == 'w0208-selected'] == ['2.0.0']
                result['checks']['reload_components_match_version'] = any(c['name'].endswith('-v2') for c in current['descriptions']) and current['hooks'][0]['commandText'] == 'exit 8'
                result['observations']['native_local_install_freezes_old_version'] = False
            if mode == 'same-name':
                result['checks']['duplicate_names_require_path_validation'] = len([p for p in result['reloaded']['plugins'] if p['name'] == 'w0208-selected']) == 2 and len(current['agents']) == 1 and len(current['hooks']) == 1
            if mode.startswith('installed-'):
                sources = [p['source'] for p in result['reloaded']['plugins'] if p['name'] == 'w0208-selected']
                expected = [] if mode == 'installed-disabled' else ['w0208-selected@w0208-local']
                if mode in ['installed-and-inline', 'installed-source-disabled', 'installed-duplicate']:
                    expected = ['w0208-selected@inline']
                result['checks']['installed_source_identity'] = sorted(sources) == sorted(expected)
                if mode == 'installed-duplicate':
                    result['checks']['inline_name_shadows_different_installed_path'] = [p['version'] for p in result['reloaded']['plugins'] if p['name'] == 'w0208-selected'] == ['9.0.0']
            result['observations']['selected_components_absent_after_reload'] = all(not rows for rows in current.values())
        if mode == 'skip-mcp':
            result['checks']['mcp_discovery_not_launched'] = not sentinel.exists()
        if mode in ['enabled', 'flag-disabled', 'invalid-manifest', 'skip-mcp', 'installed-omitted'] and not result.get('error'):
            process = subprocess.Popen(['node', str(Path(__file__).with_suffix('.mjs')), str(case / 'request.json')],
                cwd=Path(__file__).resolve().parent.parent, env=env, text=True,
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
            try:
                stdout, stderr = process.communicate(timeout=45)
                write(case / 'sdk-stdout.log', stdout)
                write(case / 'sdk-stderr.log', stderr)
            finally:
                stop(process)
            sdk = json.loads((case / 'sdk-result.json').read_text()) if (case / 'sdk-result.json').exists() else {}
            result['checks']['sdk_public_api_completed'] = process.returncode == 0 and bool(sdk) and not sdk.get('error')
            if result['checks']['sdk_public_api_completed']:
                result['checks']['sdk_public_api_matches_native_inventory'] = (
                    sdk['initialized']['commands'] == result['initialized']['commands'] and
                    sdk['initialized']['agents'] == result['initialized']['agents'] and
                    sdk['initialized'].get('plugins_applied') == result['initialized'].get('plugins_applied') and
                    sdk['reloaded']['plugins'] == result['reloaded']['plugins'] and
                    sdk['reloaded']['error_count'] == result['reloaded']['error_count'])
                result['checks']['sdk_no_input_or_tool_execution'] = sdk['no_user_input'] and sdk['model_results'] == sdk['permission_calls'] == 0
                result['checks']['sdk_owned_cli_stopped'] = sdk['child_stopped']
            if mode == 'skip-mcp':
                result['checks']['sdk_mcp_discovery_not_launched'] = not sentinel.exists()
        write(case / 'result.json', result)
        reports[mode] = result
        write(root / 'result.json', {'cases': reports, 'scope': 'isolated native plugin wire contracts; zero user input; no model/permission/tool execution',
                                    'production_selection_verified': False, 'hook_execution_verified': False,
                                    'expectations_met': all(all(r['checks'].values()) for r in reports.values())})
        print(mode, 'checks=' + str(all(result['checks'].values())), 'error=' + result.get('error', ''), flush=True)
    return all(all(r['checks'].values()) for r in reports.values())


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--case', action='append', choices=MODES)
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='netcap-w0208-plugin-contract-'))
    print('Evidence directory:', root, flush=True)
    raise SystemExit(0 if run(root, args.case or MODES) else 1)
