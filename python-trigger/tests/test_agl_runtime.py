"""Run the car's actual Python 2 against a workstation mock via SSH forwarding."""
import json
import argparse
import re
import shlex
import subprocess
import tempfile
import threading
import time
from pathlib import Path
from http.server import ThreadingHTTPServer
import test_trigger as mock

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--target', required=True, help='SSH destination, e.g. root@HEADUNIT')
    parser.add_argument('--python', default='python2.7', help='Remote Python executable (default: python2.7)')
    args = parser.parse_args()
    target = args.target
    if target.startswith('-') or any(c.isspace() for c in target):
        parser.error('invalid SSH target')
    remote = None
    server = ThreadingHTTPServer(('127.0.0.1', 0), mock.Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    port = server.server_port
    base = 'http://127.0.0.1:%d/smartlight/' % port
    records = []
    try:
        with tempfile.TemporaryDirectory(prefix='smartlight-ssh-') as temp:
            control = str(Path(temp) / 'control')
            ssh = ['ssh', '-S', control, '-o', 'ConnectTimeout=10', target]
            subprocess.run(['ssh', '-M', '-S', control, '-fN', '-o', 'ExitOnForwardFailure=yes',
                            '-o', 'ConnectTimeout=10', '-R', '%d:127.0.0.1:%d' % (port, port), target], check=True)
            try:
                result = subprocess.run(ssh + ['mktemp -d /tmp/smartlight-mock.XXXXXX'],
                                        capture_output=True, text=True, check=True)
                remote = result.stdout.strip()
                if not re.fullmatch(r'/tmp/smartlight-mock\.[A-Za-z0-9]+', remote):
                    raise RuntimeError('unexpected remote staging path')
                subprocess.run(['scp', '-O', '-o', 'ControlPath=' + control, '-r'] +
                               [str(ROOT/name) for name in ('smartlight_trigger.py', 'agl_runtime.py', 'vendor')] +
                               [target + ':' + remote + '/'], check=True)
                options_python = args.python
                def run(name, args, expected=0, injected=None):
                    args = list(args)
                    if args[0] == 'trigger' and '--apply' in args and '--state' not in args:
                        args += ['--state', remote + '/run-%d.json' % len(records)]
                    argv = [options_python, remote + '/smartlight_trigger.py'] + args
                    if injected:
                        argv = [options_python, '-c', injected] + args
                    result = subprocess.run(ssh + [shlex.join(argv)], capture_output=True, text=True, timeout=25)
                    fixture = mock.snapshot()
                    records.append({'name': name, 'args': args, 'exit_status': result.returncode,
                                    'stdout': result.stdout, 'stderr': result.stderr,
                                    'mock_requests': fixture['requests'], 'mock_mutations': fixture['writes']})
                    assert result.returncode == expected, records[-1]
                    return result, fixture

                mock.reset()
                result, fixture = run('list', ['list', '--base', base])
                assert not fixture['writes']
                mock.reset(pagination='normal')
                result, fixture = run('pagination', ['list', '--base', base])
                assert len(json.loads(result.stdout.splitlines()[-1])['data']) == 2
                mock.reset(pagination='cross')
                result, fixture = run('cross_origin_pagination', ['list', '--base', base], 1)
                assert len(fixture['requests']) == 1 and not fixture['writes']
                baseline = mock.reset()
                result, fixture = run('dry_run', ['trigger', '--base', base, '--purpose', 'fixture-nav-effect'])
                assert not fixture['writes'] and fixture['queue'] == baseline
                baseline = mock.reset()
                state = remote + '/mock-state.json'
                result, fixture = run('apply_and_cleanup_on_mock', ['trigger', '--base', base, '--purpose',
                                      'fixture-nav-effect', '--hold', '0.1', '--apply', '--state', state])
                assert fixture['queue'] == baseline
                assert [r['method'] for r in fixture['writes']] == ['POST', 'DELETE']
                result, fixture = run('idempotent_cleanup', ['cleanup', '--state', state])
                assert len(fixture['writes']) == 2
                for direction, name in [('left', '15'), ('right', '14'), ('左转', '15'), ('右转', '14')]:
                    baseline = mock.reset(omit_purpose_ref=True)
                    result, fixture = run('direction_' + direction, ['trigger', '--base', base, '--direction', direction,
                                                                    '--apply', '--hold', '0.1'])
                    assert fixture['queue'] == baseline
                    assert fixture['writes'][0]['body']['name'] == name
                baseline = mock.reset(concurrent_same_name=True)
                result, fixture = run('same_name_preserved', ['trigger', '--base', base, '--direction', 'left',
                                                            '--apply', '--hold', '0.1'])
                assert len(fixture['queue']) == len(baseline) + 1 and fixture['queue'][-1]['id'] == 'other'
                mock.reset(no_location=True)
                result, fixture = run('missing_location_no_unsafe_delete', ['trigger', '--base', base,
                                                                          '--direction', 'left', '--apply'], 1)
                assert [r['method'] for r in fixture['writes']] == ['POST']
                mock.reset(reject_post=True)
                result, fixture = run('rejected_post', ['trigger', '--base', base, '--direction', 'left', '--apply'], 1)
                assert not fixture['writes'] and 'cleanup_pending' not in result.stdout
                result, fixture = run('state_file_exclusive', ['trigger', '--base', base, '--purpose',
                                      'fixture-nav-effect', '--apply', '--state', state], 1)
                assert not fixture['writes']
                for option in ('post_error', 'non_json_create'):
                    baseline = mock.reset(**{option: True})
                    result, fixture = run(option, ['trigger', '--base', base, '--purpose', 'fixture-nav-effect',
                                                 '--apply', '--state', state + '-' + option], 1)
                    assert fixture['queue'] == baseline
                    assert [r['method'] for r in fixture['writes']] == ['POST', 'DELETE']
                mock.reset(redirect=True)
                result, fixture = run('redirect', ['list', '--base', base], 1)
                assert len(fixture['requests']) == 1
                baseline = mock.reset()
                injected = ('import sys; sys.path.insert(0, ' + repr(remote) + '); '
                            'import smartlight_trigger as m; '
                            'm.time.sleep=lambda seconds: (_ for _ in ()).throw(m.Interrupted("test interruption")); '
                            'sys.exit(m.main())')
                result, fixture = run('interruption_cleanup', ['trigger', '--base', base, '--purpose',
                                      'fixture-nav-effect', '--apply', '--state', state + '-interrupted'],
                                      130, injected=injected)
                assert fixture['queue'] == baseline
                assert [r['method'] for r in fixture['writes']] == ['POST', 'DELETE']
                mock.reset(auth='Bearer mock-token')
                credential_code = ('import os; p=' + repr(remote + '/mock-auth.txt') + '; '
                                   'f=os.fdopen(os.open(p, os.O_WRONLY|os.O_CREAT|os.O_TRUNC, 384), "w"); '
                                   'f.write("Bearer mock-token\\n"); f.close()')
                subprocess.run(ssh + [shlex.join([options_python, '-c', credential_code])], check=True)
                result, fixture = run('normal_auth', ['probe', '--base', base,
                                                     '--auth-file', remote + '/mock-auth.txt'])
                assert len(fixture['requests']) == 6 and not fixture['writes']
                baseline = mock.reset(post_delay=0.8)
                injected = ('import sys,os; sys.path.insert(0, ' + repr(remote) + '); '
                            'import smartlight_trigger as m; f=open(' + repr(remote + '/signal.pid') + ', "w"); '
                            'f.write(str(os.getpid())); f.close(); sys.exit(m.main())')
                signal_args = ['trigger', '--base', base, '--direction', 'left', '--apply', '--state', state + '-signal']
                command = ssh + [shlex.join([options_python, '-c', injected] + signal_args)]
                process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                seen_state = False
                try:
                    deadline = time.time() + 8
                    while time.time() < deadline:
                        ready = subprocess.run(ssh + ['test -e ' + shlex.quote(state + '-signal')], capture_output=True)
                        if ready.returncode == 0:
                            seen_state = True
                            break
                        time.sleep(0.05)
                    assert seen_state, 'POST state was not created'
                    time.sleep(0.2)
                    subprocess.run(ssh + ['kill -TERM "$(cat ' + shlex.quote(remote + '/signal.pid') + ')"'], check=True)
                    stdout, stderr = process.communicate(timeout=15)
                    records.append({'name': 'signal_during_post', 'exit_status': process.returncode,
                                    'stdout': stdout, 'stderr': stderr, 'mock_mutations': mock.snapshot()['writes']})
                    assert process.returncode == 130 and mock.snapshot()['queue'] == baseline, records[-1]
                    assert [r['method'] for r in mock.snapshot()['writes']] == ['POST', 'DELETE']
                finally:
                    if process.poll() is None:
                        process.terminate()
                        process.wait(timeout=5)
                print(json.dumps({'passed': len(records), 'total': len(records),
                                  'runtime': options_python + ', remote interpreter and workstation loopback mock'}, indent=2))
            finally:
                if remote is not None and re.fullmatch(r'/tmp/smartlight-mock\.[A-Za-z0-9]+', remote):
                    subprocess.run(ssh + ['rm -rf -- ' + shlex.quote(remote)], capture_output=True)
                subprocess.run(['ssh', '-S', control, '-O', 'exit', target], capture_output=True)
    finally:
        server.shutdown()
        server.server_close()
        (ROOT / 'artifacts').mkdir(exist_ok=True)
        (ROOT / 'artifacts' / 'python2-mock.json').write_text(json.dumps({
            'kind': 'vehicle-python2-workstation-mock-only',
            'note': 'All requests use the workstation mock via SSH forwarding; no vehicle-service mutations.',
            'runs': records}, indent=2) + '\n')


if __name__ == '__main__':
    main()
