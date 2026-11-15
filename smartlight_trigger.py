#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""Short SmartLight purpose request, Python 2.7 / 3 with curl and local parsers.

Uses the S2995 smartlight.queuedpurposes contract. No CAN writes, firmware
patches, feature-switch changes, brightness changes, or authentication bypass.
"""
from __future__ import print_function

import codecs
import os
import re
import signal
import sys
import time

# Yocto's minimal Python image lacks JSON and URL parsing as well as networking.
# Keep the unmodified CPython 2.7 sources private to this application.
if sys.version_info[0] == 2:
    sys.path.append(os.path.join(os.path.dirname(os.path.abspath(__file__)), 'vendor'))
import json
from agl_runtime import curl_request, random_hex, temporary_parent, unquote
try:
    from urllib.parse import urlsplit as _urlsplit, urljoin as _urljoin
except ImportError:
    from urlparse import urlsplit as _urlsplit, urljoin as _urljoin

try:
    STRING = (basestring,)
except NameError:
    STRING = (str,)

class Failure(Exception):
    pass

class Interrupted(Exception):
    pass

def ascii_uri(value):
    # Service URIs are ASCII. Python 2's Unicode authority checks otherwise
    # require unicodedata, another absent extension in the minimal image.
    try:
        encoded = value.encode('ascii')
    except UnicodeError:
        raise Failure('Service URI must use ASCII/percent encoding')
    if any(c in value for c in ('\\', '\r', '\n', '\t', '\0')):
        raise Failure('Invalid character in service URI')
    return encoded if sys.version_info[0] == 2 else value

def urlsplit(value):
    return _urlsplit(ascii_uri(value))

def urljoin(base, value):
    return _urljoin(ascii_uri(base), ascii_uri(value))

def emit(event, **fields):
    fields['event'] = event
    print(json.dumps(fields, ensure_ascii=True, sort_keys=True))
    sys.stdout.flush()

def origin(url):
    p = urlsplit(url)
    return (p.scheme.lower(), (p.hostname or '').lower(),
            p.port or (443 if p.scheme == 'https' else 80))

def base_url(value):
    p = urlsplit(value)
    if (p.scheme not in ('http', 'https') or not p.hostname or
            p.username or p.password or p.query or p.fragment):
        raise Failure('BASE must be an HTTP(S) service URI without userinfo/query/fragment')
    if any(part in ('.', '..') for part in unquote(p.path).split('/')):
        raise Failure('BASE path traversal rejected')
    return value.rstrip('/') + '/'

def items(payload):
    """Admit direct arrays/elements and VIWI {status,data:[...]} envelopes."""
    if isinstance(payload, dict) and 'status' in payload:
        if payload['status'] not in ('success', 'ok', 'OK', 200, 201, 204):
            raise Failure('API status is not success: %s' % payload['status'])
    value = payload.get('data') if isinstance(payload, dict) and 'data' in payload else payload
    if isinstance(value, list) and all(isinstance(x, dict) for x in value):
        return value
    if isinstance(value, dict) and any(k in value for k in ('id', 'uri', 'name')):
        return [value]
    raise Failure('Unknown response envelope; inspect saved probe response before adapting it')

def write_state(path, state, exclusive=False):
    parent = os.path.dirname(os.path.abspath(path))
    if not os.path.isdir(parent):
        raise Failure('State parent directory does not exist: %s' % parent)
    target = path if exclusive else path + '.next-' + random_hex()
    fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(fd, 'w') as f:
            f.write(json.dumps(state, ensure_ascii=True, sort_keys=True, indent=2) + '\n')
            f.flush()
            os.fsync(f.fileno())
        if not exclusive:
            os.rename(target, path)
    finally:
        if not exclusive and os.path.exists(target):
            os.unlink(target)

class Client(object):
    def __init__(self, base, auth=None, timeout=3.0, advertised_base=None):
        self.base = base_url(base)
        self.advertised_base = base_url(advertised_base) if advertised_base else self.base
        self.auth = auth
        self.timeout = timeout

    def resource(self, name):
        return self.base + name + '/'

    def checked(self, value, resource):
        if not isinstance(value, STRING) or not value:
            raise Failure('Missing resource URI: %s' % resource)
        # Explicit origin/prefix mapping for a tunnel whose server advertises QNX loopback.
        absolute = urljoin(self.advertised_base, value)
        if absolute.startswith(self.advertised_base):
            absolute = self.base + absolute[len(self.advertised_base):]
        p = urlsplit(absolute)
        expected_path = urlsplit(self.resource(resource)).path
        decoded = unquote(p.path)
        if (origin(absolute) != origin(self.base) or p.username or p.password or
                p.query or p.fragment or not decoded.startswith(expected_path) or
                not decoded[len(expected_path):].strip('/') or
                any(part in ('.', '..') for part in decoded.split('/'))):
            raise Failure('Cross-origin or out-of-resource URI rejected: %s' % resource)
        return absolute

    def request(self, method, url, body=None, missing_ok=False, response_hook=None):
        headers = {'Accept': 'application/json'}
        data = None
        if body is not None:
            data = json.dumps(body, ensure_ascii=True).encode('utf-8')
            headers['Content-Type'] = 'application/json'
        if self.auth:
            headers['Authorization'] = self.auth
        try:
            code, raw, location = curl_request(method, url, headers, data, self.timeout)
        except (RuntimeError, IOError, OSError) as exc:
            emit('transport_error', method=method)
            raise Failure('Transport error on %s (%s); no automatic mutation retry' %
                          (method, str(exc)))
        emit('http', method=method, status=code)
        if response_hook:
            response_hook(code, location)
        if missing_ok and code in (404, 410):
            return code, None, None
        if not 200 <= code < 300:
            detail = ''
            try:
                error = json.loads(raw.decode('utf-8'))
                message = error.get('message') if isinstance(error, dict) else None
                if isinstance(message, STRING):
                    detail = ': ' + message[:256]
            except (ValueError, UnicodeError):
                pass
            raise Failure('HTTP %s on %s%s; no automatic mutation retry' % (code, method, detail))
        if len(raw) > 2 * 1024 * 1024:
            raise Failure('Response exceeds 2 MiB')
        if not raw.strip():
            return code, None, location
        try:
            payload = json.loads(raw.decode('utf-8'))
        except (ValueError, UnicodeError):
            raise Failure('Response is not UTF-8 JSON')
        if isinstance(payload, dict) and 'status' in payload:
            if payload['status'] not in ('success', 'ok', 'OK', 200, 201, 204):
                raise Failure('API status is not success: %s' % payload['status'])
        return code, payload, location

    def collection(self, resource):
        url = self.resource(resource)
        seen = set()
        rows = []
        while url:
            if url in seen or len(seen) >= 100:
                raise Failure('Collection pagination repeats or exceeds 100 pages')
            seen.add(url)
            payload = self.request('GET', url)[1]
            rows.extend(items(payload))
            paging = payload.get('paging') if isinstance(payload, dict) else None
            if paging is not None and not isinstance(paging, dict):
                raise Failure('Unknown collection paging envelope')
            following = paging.get('next') if paging else None
            if not following:
                break
            if not isinstance(following, STRING):
                raise Failure('Invalid collection paging URI')
            absolute = urljoin(self.advertised_base, following)
            if absolute.startswith(self.advertised_base):
                absolute = self.base + absolute[len(self.advertised_base):]
            p = urlsplit(absolute)
            decoded = unquote(p.path)
            if (origin(absolute) != origin(self.base) or p.username or p.password or
                    p.fragment or decoded.rstrip('/') != urlsplit(self.resource(resource)).path.rstrip('/') or
                    any(part in ('.', '..') for part in decoded.split('/'))):
                raise Failure('Cross-origin or out-of-resource pagination rejected')
            url = absolute
        return rows

    def element_uri(self, row, resource):
        if row.get('uri'):
            return self.checked(row['uri'], resource)
        # Resource IDs are opaque: do not guess an element URL from id alone.
        raise Failure('Element lacks uri: %s' % resource)

def own_rows(client, state):
    if state.get('kind') == 'smartlight-short-request-v2':
        target = state.get('queue_uri')
        if not target:
            if state['phase'] == 'rejected':
                return []
            raise Failure('POST identity unknown: no saved Location URI. Retain state; '
                          'never delete by the shared purpose name')
        target = client.checked(target, 'queuedpurposes')
        if target in state['baseline_queue_uris']:
            raise Failure('Cleanup identity overlaps a pre-existing request')
        result = []
        for row in client.collection('queuedpurposes'):
            uri = client.element_uri(row, 'queuedpurposes')
            if uri != target:
                continue
            if row.get('name') != state['request_name']:
                raise Failure('Recorded queue URI has a different name')
            purpose = row.get('purpose')
            if isinstance(purpose, dict):
                purpose = purpose.get('uri')
            if purpose and client.checked(purpose, 'purposes') != state['purpose_uri']:
                raise Failure('Cleanup purpose mismatch')
            result.append(uri)
        return result
    result = []
    for row in client.collection('queuedpurposes'):
        if row.get('name') != state['request_name']:
            continue
        uri = client.element_uri(row, 'queuedpurposes')
        if uri in state['baseline_queue_uris']:
            raise Failure('Cleanup identity overlaps a pre-existing request')
        purpose = row.get('purpose')
        if isinstance(purpose, dict):
            purpose = purpose.get('uri')
        if purpose and client.checked(purpose, 'purposes') != state['purpose_uri']:
            raise Failure('Cleanup purpose mismatch')
        result.append(uri)
    if len(result) > 1:
        raise Failure('Duplicate request identity; retaining state for inspection')
    return result

def cleanup(client, state, path, confirm_absent=False):
    """Delete only the server-assigned queue URI; support old unique-name states."""
    kind = state.get('kind')
    if kind not in ('smartlight-short-request-v1', 'smartlight-short-request-v2'):
        raise Failure('Unrecognized cleanup state format')
    name = state.get('request_name')
    if not isinstance(name, STRING) or not name:
        raise Failure('Invalid recorded request name')
    if kind == 'smartlight-short-request-v1' and not re.match(r'^agl-light-[0-9a-f]{32}$', name):
        raise Failure('Invalid recorded request identity')
    if base_url(state['base']) != client.base:
        raise Failure('Cleanup service differs from recorded service')
    if base_url(state.get('advertised_base', state['base'])) != client.advertised_base:
        raise Failure('Cleanup advertised service differs from recorded service')
    if state['phase'] == 'cleaned':
        emit('cleanup', result='already_cleaned', state=path)
        return
    client.checked(state['purpose_uri'], 'purposes')
    candidates = own_rows(client, state)
    if kind == 'smartlight-short-request-v2' and state.get('queue_uri'):
        # A server can remove a one-shot item before the first queue read.
        # Still revoke exactly the URI returned by this POST; 404/410 are fine.
        candidates = [client.checked(state['queue_uri'], 'queuedpurposes')]
    if not candidates and state['phase'] == 'creating' and not confirm_absent:
        raise Failure('POST outcome uncertain; keep state. Recheck after server settles; '
                      'cleanup --confirm-absent permits confirming an empty queue then')
    for uri in candidates:
        client.request('DELETE', uri, missing_ok=True)
    if own_rows(client, state):
        raise Failure('Request still visible after DELETE; cleanup needs another attempt')
    state['phase'] = 'cleaned'
    state['cleanup_time'] = time.time()
    write_state(path, state)
    emit('cleanup', result='confirmed_absent', deleted=len(candidates), state=path)

def select_purpose(client, args):
    rows = client.collection('purposes')
    selected = []
    for row in rows:
        uri = client.element_uri(row, 'purposes')
        if args.direction:
            value = row.get('functionValue')
            match = (row.get('functionCategory') == 'navigation' and
                     row.get('functionName') == 'direction' and
                     value in (args.direction, [args.direction]))
        elif args.purpose_uri:
            match = uri == client.checked(args.purpose_uri, 'purposes')
        else:
            match = args.purpose in (row.get('id'), row.get('name'))
        if match:
            selected.append(row)
    if len(selected) != 1:
        raise Failure('Select exactly one real purpose by direction/name/id/uri; matches=%s' % len(selected))
    return selected[0]

def preflight(client, purpose):
    configurations = client.collection('configurations')
    if any(x.get('smartlightState') in ('unavailable', 'defect') for x in configurations):
        raise Failure('SmartLight reports unavailable/defect')
    groups = client.collection('settinggroups')
    ref = purpose.get('settingGroup')
    if isinstance(ref, dict):
        if ref.get('isActivated') is False:
            raise Failure('Selected setting group is off; enable the intended feature in HMI first')
        ids = [ref.get('id'), ref.get('name'), ref.get('uri')]
    else:
        ids = [ref]
    matched = [g for g in groups if any(v and v in (g.get('id'), g.get('name'), g.get('uri')) for v in ids)]
    if any(g.get('isActivated') is False for g in matched):
        raise Failure('Selected setting group is off; enable the intended feature in HMI first')
    emit('preflight', configuration_states=[x.get('smartlightState', 'unknown') for x in configurations],
         matched_groups=len(matched), switches_changed=False, brightness_changed=False)

def trigger(client, args):
    purpose = select_purpose(client, args)
    purpose_uri = client.element_uri(purpose, 'purposes')
    preflight(client, purpose)
    baseline = client.collection('queuedpurposes')
    baseline_uris = [client.element_uri(x, 'queuedpurposes') for x in baseline]
    request_name = purpose.get('name')
    if not isinstance(request_name, STRING) or not request_name:
        raise Failure('Selected purpose has no real name')
    # This firmware validates the purpose name. A random client name yields 400.
    body = {'name': request_name, 'purpose': urlsplit(purpose_uri).path}
    emit('plan', apply=args.apply, purpose_name=purpose.get('name'),
         purpose_uri=purpose_uri, direction=args.direction, hold_seconds=args.hold,
         physical_light_verified=False)
    if not args.apply:
        emit('dry_run', mutations=0)
        return
    if any(row.get('name') == request_name for row in baseline):
        raise Failure('This purpose is already queued; leave the existing request untouched')
    state = {'kind': 'smartlight-short-request-v2', 'phase': 'creating',
             'base': client.base, 'request_name': request_name,
             'run_id': random_hex(), 'queue_uri': None, 'direction': args.direction,
             'advertised_base': client.advertised_base,
             'purpose_uri': purpose_uri, 'baseline_queue_uris': baseline_uris,
             'created_time': time.time(), 'hold_seconds': args.hold}
    # Durable intent precedes mutation; refuse overwriting a previous run's state.
    write_state(args.state, state, exclusive=True)
    emit('state_saved', state=args.state)
    failure = None
    pending_signal = []
    def defer_interrupt(signum, frame):
        pending_signal.append(signum)
    def record_response(code, location):
        state['http_status'] = code
        if location:
            uri = client.checked(location, 'queuedpurposes')
            if uri in baseline_uris:
                raise Failure('POST Location overlaps a pre-existing request')
            state['queue_uri'] = uri
        if 200 <= code < 300:
            state['phase'] = 'posted'
        elif code in (400, 401, 403, 404, 405, 415, 422) and not location:
            state['phase'] = 'rejected'
        write_state(args.state, state)
    try:
        # Finish this bounded POST and save its Location before handling a signal.
        previous_handlers = {}
        for sig in (signal.SIGINT, signal.SIGTERM):
            previous_handlers[sig] = signal.signal(sig, defer_interrupt)
        try:
            client.request('POST', client.resource('queuedpurposes'), body,
                           response_hook=record_response)
        finally:
            for sig, handler in previous_handlers.items():
                signal.signal(sig, handler)
        if pending_signal:
            raise Interrupted('Signal %s during POST' % pending_signal[0])
        if not state['queue_uri']:
            raise Failure('Successful POST did not return a Location URI; retaining state')
        created = own_rows(client, state)
        if created:
            emit('request_observed', queue_item_uri=created[0])
        else:
            emit('request_not_visible', detail='May have completed/rejected/dropped; physical effect unverified')
        players = client.collection('players')
        emit('players_snapshot', players=players, physical_light_verified=False)
        until = time.time() + args.hold
        while time.time() < until:
            time.sleep(min(0.2, max(0.0, until - time.time())))
    except (Failure, Interrupted, KeyboardInterrupt, IOError, OSError) as exc:
        failure = exc
    finally:
        # A second Ctrl-C/SIGTERM must not abort this bounded cleanup attempt.
        for sig in (signal.SIGINT, signal.SIGTERM):
            signal.signal(sig, signal.SIG_IGN)
        try:
            cleanup(client, state, args.state)
        except (Failure, IOError, OSError, ValueError, KeyError) as exc:
            emit('cleanup_pending', state=args.state, error=str(exc))
            raise Failure('Cleanup pending; run cleanup with this state file and service credentials')
    if failure is not None:
        raise failure

class Options(object):
    pass

class CommandLine(object):
    """Small exact-option parser for stripped AGL Python (no argparse/optparse)."""
    HELP = '''Usage: smartlight_trigger.py {list,probe,trigger,cleanup} [options]

  --base URI               SmartLight service base, including /smartlight/
  --advertised-base URI    Explicit URI prefix mapping for a proxy/tunnel
  --auth-file PATH         Complete Authorization value in a one-line file
  --purpose NAME_OR_ID     Exact purpose name/resource id from list
  --direction left|right   Navigation turn; also accepts Chinese left/right
  --purpose-uri URI        Exact purpose URI (exclusive with --purpose)
  --hold SECONDS           Wait before cleanup, 0.1..5 (default: 2)
  --timeout SECONDS        Per-request timeout, 0.1..15 (default: 3)
  --state PATH             Run-specific durable state file
  --apply                  Actually create one request; default is read-only
  --confirm-absent         cleanup: confirm settled absence after uncertain POST
  -h, --help               Show this help

QNX loopback is not AGL loopback. Use HTTP when the service advertises HTTP.
'''

    def error(self, message):
        sys.stderr.write('Argument error: ' + message + '\n')
        sys.stderr.write(self.HELP)
        raise SystemExit(2)

    def parse_args(self, argv=None):
        tokens = list(sys.argv[1:] if argv is None else argv)
        values = {'action': None, 'base': None, 'advertised_base': None, 'direction': None,
                  'auth_file': None, 'purpose': None, 'purpose_uri': None,
                  'hold': 2.0, 'timeout': 3.0, 'state': None,
                  'apply': False, 'confirm_absent': False}
        valued = {'--base': 'base', '--advertised-base': 'advertised_base',
                  '--auth-file': 'auth_file', '--purpose': 'purpose', '--direction': 'direction',
                  '--purpose-uri': 'purpose_uri', '--hold': 'hold',
                  '--timeout': 'timeout', '--state': 'state'}
        flags = {'--apply': 'apply', '--confirm-absent': 'confirm_absent'}
        i = 0
        while i < len(tokens):
            token = tokens[i]
            if token in ('-h', '--help'):
                sys.stdout.write(self.HELP)
                raise SystemExit(0)
            option, equals, inline = token.partition('=')
            if option in flags:
                if equals:
                    self.error(option + ' takes no value')
                values[flags[option]] = True
            elif option in valued:
                if equals:
                    value = inline
                else:
                    i += 1
                    if i == len(tokens) or tokens[i].startswith('--'):
                        self.error(option + ' requires a value')
                    value = tokens[i]
                if not value:
                    self.error(option + ' requires a nonempty value')
                key = valued[option]
                if key in ('hold', 'timeout'):
                    try:
                        value = float(value)
                    except ValueError:
                        self.error(option + ' requires a number')
                values[key] = value
            elif token.startswith('-'):
                self.error('unknown option: ' + token)
            elif values['action'] is None and token in ('list', 'probe', 'trigger', 'cleanup'):
                values['action'] = token
            else:
                self.error('unexpected action/argument: ' + token)
            i += 1
        if values['action'] is None:
            self.error('one action is required')
        if sum(values[k] is not None for k in ('purpose', 'purpose_uri', 'direction')) > 1:
            self.error('--direction, --purpose and --purpose-uri are mutually exclusive')
        if values['direction'] is not None:
            direction = values['direction']
            if not isinstance(direction, type(u'')):
                direction = direction.decode('utf-8')
            aliases = {'left': 'left', 'right': 'right', u'左转': 'left', u'右转': 'right'}
            if direction not in aliases:
                self.error('--direction must be left, right, 左转 or 右转')
            values['direction'] = aliases[direction]
            if values['action'] != 'trigger':
                self.error('--direction is only valid with trigger')
        result = Options()
        for key, value in values.items():
            setattr(result, key, value)
        return result

def parser():
    return CommandLine()

def main(argv=None):
    args = parser().parse_args(argv)
    def interrupt(signum, frame):
        raise Interrupted('Signal %s' % signum)
    signal.signal(signal.SIGINT, interrupt)
    signal.signal(signal.SIGTERM, interrupt)
    try:
        if not (0.1 <= args.timeout <= 15 and 0.1 <= args.hold <= 5):
            raise Failure('timeout must be 0.1..15 and hold 0.1..5 seconds')
        auth = None
        if args.auth_file:
            with codecs.open(args.auth_file, 'r', encoding='utf-8') as f:
                auth = f.read().strip()
            if not auth or '\n' in auth or '\r' in auth:
                raise Failure('Authorization file must contain exactly one nonempty header value')
        if args.action == 'cleanup':
            if not args.state:
                raise Failure('cleanup requires --state')
            with codecs.open(args.state, 'r', encoding='utf-8') as f:
                state = json.load(f)
            client = Client(args.base or state['base'], auth, args.timeout,
                            args.advertised_base or state.get('advertised_base'))
            cleanup(client, state, args.state, args.confirm_absent)
            return 0
        if not args.base:
            raise Failure('--base is required; use the service URI reachable from AGL')
        client = Client(args.base, auth, args.timeout, args.advertised_base)
        if args.action == 'list':
            emit('purposes', data=client.collection('purposes'))
        elif args.action == 'probe':
            for resource in ('configurations', 'versions', 'settinggroups', 'purposes', 'queuedpurposes', 'players'):
                emit('probe', resource=resource, data=client.collection(resource))
        else:
            if not args.purpose and not args.purpose_uri and not args.direction:
                raise Failure('trigger requires --direction, --purpose or --purpose-uri')
            if not args.state:
                args.state = os.path.join(temporary_parent(), 'smartlight-' + random_hex() + '.json')
            trigger(client, args)
        return 0
    except Interrupted as exc:
        emit('interrupted', error=str(exc))
        return 130
    except (Failure, ValueError, KeyError, IOError, OSError) as exc:
        emit('error', error=str(exc))
        return 1

if __name__ == '__main__':
    sys.exit(main())
