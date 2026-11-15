"""Loopback-only mock verification, exact commands, output and mutations retained."""
from pathlib import Path
import copy
import hashlib
import json
import os
import signal
import subprocess
import sys
import tempfile
import threading
import time
import shutil
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

HERE = Path(__file__).resolve().parents[1]
SCRIPT = HERE / 'smartlight_trigger.py'
ARTIFACTS = HERE / 'artifacts'
RECORD = {'kind': 'loopback-mock-not-vehicle-test', 'runs': [], 'checks': {}}
LOCK = threading.Lock()
FIXTURE = {}

def reset(**options):
    global FIXTURE
    with LOCK:
        FIXTURE = {'queue': [{'id': 'existing', 'name': 'existing-phone-request',
                             'uri': '/smartlight/queuedpurposes/existing',
                             'purpose': {'uri': '/smartlight/purposes/phone'}, 'queuePosition': 0}],
                   'writes': [], 'requests': [], 'counter': 0, 'group_off': False,
                   'post_error': False, 'non_json_create': False, 'unknown_envelope': False,
                   'cross_uri': False, 'auth': None, 'redirect': False}
        FIXTURE['pagination'] = None
        FIXTURE['no_location'] = False
        FIXTURE['concurrent_same_name'] = False
        FIXTURE['reject_post'] = False
        FIXTURE['omit_purpose_ref'] = False
        FIXTURE['post_delay'] = 0
        FIXTURE['bad_location'] = None
        FIXTURE['advertised'] = None
        FIXTURE.update(options)
        return copy.deepcopy(FIXTURE['queue'])

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def send(self, code, payload=None, location=None, raw=None):
        data = raw if raw is not None else (json.dumps({'status': 'success', 'data': payload}).encode() if payload is not None else b'')
        self.send_response(code)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(data)))
        if location:
            self.send_header('Location', location)
        self.end_headers()
        self.wfile.write(data)

    def handle_request(self):
        path = urlsplit(self.path).path
        method = self.command
        body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
        with LOCK:
            f = FIXTURE
            # Only assert credential equality; never write the header into output.
            f['requests'].append({'method': method, 'path': path})
            if f['auth'] and self.headers.get('Authorization') != f['auth']:
                self.send(401)
                return
            if f['redirect'] and method == 'GET':
                self.send(302, location='http://127.0.0.1:1/not-followed')
                return
            if method == 'GET':
                if f['unknown_envelope']:
                    self.send(200, raw=b'{"unexpected":"envelope"}')
                    return
                if path.endswith('/purposes/'):
                    if f['pagination']:
                        mode = f['pagination']
                        offset = 1 if '$offset=1' in self.path else 0
                        row = {'id': 'page-%d' % offset, 'name': 'page-%d' % offset,
                               'uri': '/smartlight/purposes/page-%d' % offset}
                        following = '/smartlight/purposes/?$limit=1&$offset=1' if offset == 0 or mode == 'cycle' else None
                        if mode == 'cross':
                            following = 'http://127.0.0.1:1/smartlight/purposes/?$offset=1'
                        if mode == 'wrong_resource':
                            following = '/smartlight/queuedpurposes/?$offset=1'
                        self.send(200, raw=json.dumps({'status': 'ok', 'data': [row],
                                                     'paging': {'next': following}}).encode())
                        return
                    uri = 'http://127.0.0.1:1/outside/purposes/nav' if f['cross_uri'] else '/smartlight/purposes/nav'
                    if f['advertised']:
                        uri = f['advertised'] + 'purposes/nav'
                    self.send(200, [{'id': 'nav-id', 'name': 'fixture-nav-effect', 'uri': uri,
                                     'functionCategory': 'navigation', 'purposeId': 99,
                                     'settingGroup': {'id': 'nav-turn', 'name': 'nav_turn'}},
                                    {'id': 'left-id', 'name': '15', 'uri': '/smartlight/purposes/left-id',
                                     'functionCategory': 'navigation', 'functionName': 'direction', 'functionValue': ['left'],
                                     'settingGroup': {'id': 'nav-turn'}},
                                    {'id': 'right-id', 'name': '14', 'uri': '/smartlight/purposes/right-id',
                                     'functionCategory': 'navigation', 'functionName': 'direction', 'functionValue': ['right'],
                                     'settingGroup': {'id': 'nav-turn'}}])
                elif path.endswith('/configurations/'):
                    self.send(200, [{'id': 'config', 'uri': '/smartlight/configurations/config',
                                     'smartlightState': 'ready', 'brightness': 50}])
                elif path.endswith('/settinggroups/'):
                    self.send(200, [{'id': 'nav-turn', 'name': 'nav_turn',
                                     'uri': '/smartlight/settinggroups/nav-turn',
                                     'isActivated': not f['group_off']}])
                elif path.endswith('/queuedpurposes/'):
                    self.send(200, copy.deepcopy(f['queue']))
                elif path.endswith('/players/'):
                    self.send(200, [{'id': 'player', 'name': 'default', 'currentlyPlayedPurpose':
                                     f['queue'][-1] if len(f['queue']) > 1 else f['queue'][0]}])
                elif path.endswith('/versions/'):
                    self.send(200, [{'id': 'version', 'name': 'playerServiceSw', 'version': 'mock'}])
                else:
                    self.send(404)
            elif method == 'POST' and path.endswith('/queuedpurposes/'):
                data = json.loads(body)
                contract = {'fixture-nav-effect': '/smartlight/purposes/nav',
                            '15': '/smartlight/purposes/left-id', '14': '/smartlight/purposes/right-id'}
                if f['reject_post'] or contract.get(data.get('name')) != data.get('purpose'):
                    self.send(400, raw=b'{"status":"error","message":"invalid request"}')
                    return
                f['counter'] += 1
                row = {'id': 'created-%s' % f['counter'], 'name': data['name'],
                       'uri': '/smartlight/queuedpurposes/created-%s' % f['counter'],
                       'purpose': {'uri': data['purpose']}, 'queuePosition': 0}
                f['queue'].append(row)
                if f['omit_purpose_ref']:
                    row.pop('purpose')
                if f['concurrent_same_name']:
                    f['queue'].append(dict(row, id='other', uri='/smartlight/queuedpurposes/other'))
                f['writes'].append({'method': method, 'path': path, 'body': data})
                location = None if f['no_location'] else row['uri']
                location = f['bad_location'] or location
                if f['post_delay']:
                    time.sleep(f['post_delay'])
                if f['post_error']:
                    self.send(500, location=location)
                elif f['non_json_create']:
                    self.send(201, raw=b'not-json', location=location)
                else:
                    self.send(201, location=location, raw=b'{"status":"ok"}')
            elif method == 'DELETE':
                f['writes'].append({'method': method, 'path': path})
                old = len(f['queue'])
                f['queue'] = [r for r in f['queue'] if r['uri'] != path]
                self.send(204 if len(f['queue']) != old else 404)
            else:
                self.send(405)

    do_GET = handle_request
    do_POST = handle_request
    do_DELETE = handle_request

def snapshot():
    with LOCK:
        return copy.deepcopy(FIXTURE)

def run(name, args, expected=0, env=None, prefix=None):
    cmd = [sys.executable, str(SCRIPT)] + args if prefix is None else prefix + args
    r = subprocess.run(cmd, capture_output=True, text=True, env=env)
    f = snapshot()
    RECORD['runs'].append({'name': name, 'command': cmd, 'stdin': '',
                           'stdout': r.stdout, 'stderr': r.stderr, 'exit_status': r.returncode,
                           'mock_requests': f['requests'], 'mock_mutations': f['writes'],
                           'mock_queue_after': f['queue']})
    assert r.returncode == expected, (name, r.returncode, r.stdout, r.stderr)
    return r

def check(name, value):
    RECORD['checks'][name] = bool(value)
    assert value, name

def main():
    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    base = 'http://127.0.0.1:%s/smartlight/' % server.server_port
    RECORD['fixture'] = {'base': base, 'purpose_name': 'fixture-nav-effect',
                         'baseline_request': 'existing-phone-request',
                         'note': 'All HTTP traffic in these tests stays on loopback. No firmware programs launched.'}
    try:
        with tempfile.TemporaryDirectory(prefix='smartlight-mock-') as temp:
            folder = Path(temp)
            state = folder / 'run.json'
            baseline = reset()
            run('list', ['list', '--base', base])
            check('list_no_mutation', not snapshot()['writes'])
            reset(pagination='normal')
            result = run('paginated_list', ['list', '--base', base])
            rows = json.loads(result.stdout.splitlines()[-1])['data']
            check('pagination_returns_all_rows_without_mutation', len(rows) == 2 and not snapshot()['writes'])
            for mode in ('cycle', 'cross', 'wrong_resource'):
                reset(pagination=mode)
                run('pagination_' + mode, ['list', '--base', base], expected=1)
                check('pagination_' + mode + '_rejected', not snapshot()['writes'] and len(snapshot()['requests']) <= 2)
            baseline = reset()
            run('dry_run_baseline', ['trigger', '--base', base, '--purpose', 'fixture-nav-effect'])
            check('dry_run_zero_mutations_baseline_preserved', snapshot()['queue'] == baseline and not snapshot()['writes'])

            baseline = reset()
            run('apply_auto_cleanup', ['trigger', '--base', base, '--purpose', 'fixture-nav-effect',
                                      '--hold', '0.1', '--apply', '--state', str(state)])
            f = snapshot()
            check('trigger_one_post_one_delete_and_restores_baseline',
                  [r['method'] for r in f['writes']] == ['POST', 'DELETE'] and f['queue'] == baseline)
            check('state_records_verified_cleanup', json.loads(state.read_text())['phase'] == 'cleaned')
            check('uses_real_name_relative_purpose_and_location',
                  f['writes'][0]['body'] == {'name': 'fixture-nav-effect', 'purpose': '/smartlight/purposes/nav'} and
                  json.loads(state.read_text())['queue_uri'] == base + 'queuedpurposes/created-1')
            run('idempotent_cleanup', ['cleanup', '--state', str(state)])
            check('idempotent_cleanup_adds_no_mutation', len(snapshot()['writes']) == 2)

            for direction, name in [('left', '15'), ('right', '14'), ('左转', '15'), ('右转', '14')]:
                baseline = reset()
                run('direction_' + direction, ['trigger', '--base', base, '--direction', direction,
                                               '--apply', '--hold', '0.1'])
                f = snapshot()
                check('direction_' + direction + '_real_purpose_cleanup',
                      f['writes'][0]['body']['name'] == name and f['queue'] == baseline and len(f['writes']) == 2)
            reset()
            run('direction_conflict', ['trigger', '--base', base, '--direction', 'left', '--purpose', '14'], expected=2)
            check('conflicting_selectors_no_requests', not snapshot()['requests'])
            reset()
            run('direction_invalid', ['trigger', '--base', base, '--direction', 'up'], expected=2)
            check('invalid_direction_no_requests', not snapshot()['requests'])
            baseline = reset(omit_purpose_ref=True)
            run('queue_reference_omitted', ['trigger', '--base', base, '--direction', 'left', '--apply', '--hold', '0.1'])
            check('location_cleanup_with_minimal_queue_row', snapshot()['queue'] == baseline)
            baseline = reset(concurrent_same_name=True)
            run('concurrent_same_name', ['trigger', '--base', base, '--direction', 'left', '--apply', '--hold', '0.1'])
            f = snapshot()
            check('concurrent_same_name_preserved', len(f['queue']) == len(baseline) + 1 and
                  f['queue'][-1]['id'] == 'other' and [w['path'] for w in f['writes'] if w['method'] == 'DELETE'] ==
                  ['/smartlight/queuedpurposes/created-1'])
            reset()
            with LOCK:
                FIXTURE['queue'].append({'id': 'same-name', 'name': '15', 'uri': '/smartlight/queuedpurposes/same-name'})
            run('already_queued', ['trigger', '--base', base, '--direction', 'left', '--apply'], expected=1)
            check('existing_same_name_no_mutation', not snapshot()['writes'])
            reset(reject_post=True)
            rejected = folder / 'rejected.json'
            run('rejected_post', ['trigger', '--base', base, '--direction', 'left', '--apply', '--state', str(rejected)], expected=1)
            check('explicit_rejection_not_cleanup_pending', json.loads(rejected.read_text())['phase'] == 'cleaned' and not snapshot()['writes'])
            for option in ('normal', 'post_error'):
                reset(no_location=True, **({'post_error': True} if option == 'post_error' else {}))
                missing = folder / ('missing-location-' + option + '.json')
                run('missing_location_' + option, ['trigger', '--base', base, '--direction', 'left', '--apply', '--state', str(missing)], expected=1)
                run('missing_location_cleanup_' + option, ['cleanup', '--state', str(missing), '--confirm-absent'], expected=1)
                check('missing_location_' + option + '_never_delete_by_shared_name',
                      [r['method'] for r in snapshot()['writes']] == ['POST'] and json.loads(missing.read_text())['phase'] != 'cleaned')
            for location in ('http://127.0.0.1:1/smartlight/queuedpurposes/foreign',
                             '/smartlight/queuedpurposes/existing'):
                reset(bad_location=location)
                run('invalid_location', ['trigger', '--base', base, '--direction', 'left', '--apply'], expected=1)
                check('invalid_location_' + location + '_no_delete', [r['method'] for r in snapshot()['writes']] == ['POST'])

            reset()
            run('unknown_purpose', ['trigger', '--base', base, '--purpose', 'not-found', '--apply'], expected=1)
            check('unknown_purpose_no_mutation', not snapshot()['writes'])
            reset(group_off=True)
            run('disabled_settinggroup', ['trigger', '--base', base, '--purpose', 'fixture-nav-effect', '--apply'], expected=1)
            check('group_off_no_switch_write', not snapshot()['writes'])
            reset(cross_uri=True)
            run('cross_origin_uri', ['trigger', '--base', base, '--purpose', 'fixture-nav-effect', '--apply'], expected=1)
            check('cross_origin_rejected_before_mutation', not snapshot()['writes'])
            reset(unknown_envelope=True)
            run('unknown_response_envelope', ['list', '--base', base], expected=1)
            check('unknown_envelope_no_mutation', not snapshot()['writes'])
            reset(redirect=True)
            run('redirect_rejected', ['list', '--base', base], expected=1)
            check('redirect_not_followed', len(snapshot()['requests']) == 1)

            for flag, case in [('post_error', 'post_500_after_creation'), ('non_json_create', 'post_non_json_after_creation')]:
                baseline = reset(**{flag: True})
                run(case, ['trigger', '--base', base, '--purpose', 'fixture-nav-effect', '--apply',
                           '--hold', '0.1', '--state', str(folder / (case + '.json'))], expected=1)
                check(case + '_no_post_retry_and_cleanup',
                      [r['method'] for r in snapshot()['writes']] == ['POST', 'DELETE'] and snapshot()['queue'] == baseline)

            baseline = reset(auth='Bearer mock-token')
            auth = folder / 'auth.txt'
            auth.write_text('Bearer mock-token\n')
            run('normal_authorization_header', ['probe', '--base', base, '--auth-file', str(auth)])
            check('probe_auth_six_gets_and_no_mutations', len(snapshot()['requests']) == 6 and not snapshot()['writes'])

            baseline = reset()
            env = os.environ.copy()
            env['http_proxy'] = env['HTTP_PROXY'] = 'http://127.0.0.1:1'
            run('proxy_environment_ignored', ['list', '--base', base], env=env)
            check('local_client_ignores_proxy', len(snapshot()['requests']) == 1)

            baseline = reset(advertised='http://[::1]:49520/smartlight/')
            run('explicit_advertised_base_mapping', ['trigger', '--base', base,
                 '--advertised-base', 'http://[::1]:49520/smartlight/', '--purpose', 'fixture-nav-effect',
                 '--hold', '0.1', '--apply', '--state', str(folder/'mapped.json')])
            check('advertised_loopback_maps_to_selected_origin', snapshot()['queue'] == baseline and len(snapshot()['writes']) == 2)

            baseline = reset()
            injected = ('import sys; sys.path.insert(0, '+repr(str(HERE))+'); '
                        'import smartlight_trigger as m; '
                        'm.time.sleep=lambda seconds: (_ for _ in ()).throw(m.Interrupted("injected interruption")); '
                        'sys.exit(m.main())')
            run('interrupted_hold_cleanup', ['trigger', '--base', base, '--purpose', 'fixture-nav-effect',
                  '--apply', '--state', str(folder/'interrupted.json')], expected=130,
                 prefix=[sys.executable, '-c', injected])
            check('interruption_runs_cleanup', snapshot()['queue'] == baseline and len(snapshot()['writes']) == 2)

            baseline = reset(post_delay=0.8)
            sigstate = folder / 'post-signal.json'
            command = [sys.executable, str(SCRIPT), 'trigger', '--base', base, '--direction', 'left',
                       '--apply', '--state', str(sigstate)]
            process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            deadline = time.time() + 5
            while not sigstate.exists() and process.poll() is None and time.time() < deadline:
                time.sleep(0.01)
            time.sleep(0.2)
            process.send_signal(signal.SIGTERM)
            stdout, stderr = process.communicate(timeout=10)
            RECORD['runs'].append({'name': 'sigterm_during_post', 'command': command, 'stdout': stdout,
                                   'stderr': stderr, 'exit_status': process.returncode, 'mock_mutations': snapshot()['writes']})
            check('sigterm_during_post_persists_location_and_cleans', process.returncode == 130 and
                  snapshot()['queue'] == baseline and json.loads(sigstate.read_text())['phase'] == 'cleaned')

            # Simulate previous process loss: persistent intent exists and only its request is queued.
            baseline = reset()
            recovery = {'kind': 'smartlight-short-request-v1', 'phase': 'posted', 'base': base,
                        'request_name': 'agl-light-' + 'a' * 32, 'purpose_uri': base + 'purposes/nav',
                        'baseline_queue_uris': [base + 'queuedpurposes/existing']}
            recovery_path = folder / 'recovery.json'
            recovery_path.write_text(json.dumps(recovery))
            with LOCK:
                FIXTURE['queue'].append({'id': 'recovery', 'uri': '/smartlight/queuedpurposes/recovery',
                                         'name': recovery['request_name'], 'purpose': {'uri': recovery['purpose_uri']}})
            run('standalone_rollback_engine', ['cleanup', '--state', str(recovery_path)])
            check('standalone_rollback_preserves_other_queue_item', snapshot()['queue'] == baseline)

            # Exercise the real shell rollback role, not only its Python implementation.
            shell = shutil.which('sh')
            if shell:
                baseline = reset()
                recovery_path.write_text(json.dumps(recovery))
                with LOCK:
                    FIXTURE['queue'].append({'id': 'recovery', 'uri': '/smartlight/queuedpurposes/recovery',
                                             'name': recovery['request_name'], 'purpose': {'uri': recovery['purpose_uri']}})
                shim = folder / 'python2.7'
                shim.write_text('#!/bin/sh\nexec "' + Path(sys.executable).as_posix() + '" "$@"\n')
                shim.chmod(0o700)
                env = os.environ.copy()
                env['PATH'] = str(folder) + os.pathsep + env['PATH']
                cmd = [shell, str(HERE / 'rollback.sh'), '--state', str(recovery_path)]
                r = subprocess.run(cmd, capture_output=True, text=True, env=env)
                RECORD['runs'].append({'name': 'shell_rollback', 'command': cmd, 'stdout': r.stdout,
                                       'stderr': r.stderr, 'exit_status': r.returncode,
                                       'mock_mutations': snapshot()['writes']})
                check('shell_rollback_executed_and_restores_baseline', r.returncode == 0 and snapshot()['queue'] == baseline)

            baseline = reset()
            ambiguous = dict(recovery, phase='creating')
            recovery_path.write_text(json.dumps(ambiguous))
            run('ambiguous_post_not_falsely_cleaned', ['cleanup', '--state', str(recovery_path)], expected=1)
            check('ambiguous_absence_keeps_pending_state', json.loads(recovery_path.read_text())['phase'] == 'creating')
            run('confirm_settled_empty_cleanup', ['cleanup', '--state', str(recovery_path), '--confirm-absent'])
            check('explicit_settled_absence_confirmation', json.loads(recovery_path.read_text())['phase'] == 'cleaned')

            reset()
            run('hold_bound', ['trigger', '--base', base, '--purpose', 'fixture-nav-effect', '--hold', '6', '--apply'], expected=1)
            check('hold_bound_no_mutation', not snapshot()['writes'])
            # Python 2 compatibility is exercised by the optional SSH harness.
            # lib2to3 was removed in Python 3.13; do not depend on that parser.
            compile(SCRIPT.read_text(), str(SCRIPT), 'exec')
            check('python_3_compile', True)
            manifest = json.loads((HERE/'vendor/SOURCES.json').read_text())
            check('vendored_cpython_sources_match_manifest', all(
                hashlib.sha256((HERE/'vendor'/entry['path']).read_bytes()).hexdigest() == entry['sha256']
                for entry in manifest))
    finally:
        server.shutdown()
        server.server_close()
        RECORD['artifact_sha256'] = {p.name: hashlib.sha256(p.read_bytes()).hexdigest()
                                     for p in (SCRIPT, HERE/'trigger_light.sh', HERE/'rollback.sh')}
        RECORD['runtime'] = {'executable': sys.executable, 'version': sys.version,
                             'python2_runtime_executed': False, 'agl_vehicle_executed': False}
        ARTIFACTS.mkdir(exist_ok=True)
        (ARTIFACTS/'mock-verification.json').write_text(json.dumps(RECORD, ensure_ascii=False, indent=2) + '\n', encoding='utf8')
    print(json.dumps({'checks': RECORD['checks'], 'passed': sum(RECORD['checks'].values()),
                      'total': len(RECORD['checks']), 'command_runs': len(RECORD['runs'])}, indent=2))

if __name__ == '__main__':
    main()
