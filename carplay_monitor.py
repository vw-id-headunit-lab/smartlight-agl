#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""Poll existing simplenavigation and request bounded SmartLight turn effects.

Python 2.7/3, no pip dependencies on AGL. HTTP and durable queue ownership reuse
smartlight_trigger. This is an additional input, not raw iAP2 interception.
"""
from __future__ import print_function

import codecs
import os
import signal
import sys
import time

import smartlight_trigger as light
from agl_runtime import random_hex, temporary_parent

json = light.json
Failure = light.Failure
STRING = light.STRING
INTEGER = (int,) if sys.version_info[0] == 3 else (int, long)


class PulseExpired(Exception):
    pass


class SnapshotChanged(Exception):
    pass


def turn_direction(main_type, direction_type):
    """S2995 native HandleDirection semantics; unknown/straight returns None.

    These are platform RSI values, not CPManeuverType/AMap icon numbers.
    IDs are deliberately not translated into movie numbers or resource UUIDs.
    """
    if (isinstance(main_type, bool) or not isinstance(main_type, INTEGER) or
            isinstance(direction_type, bool) or not isinstance(direction_type, INTEGER)):
        return None
    if main_type == 13:
        if direction_type in (32, 64, 96):
            return 'left'
        if direction_type in (160, 192, 224):
            return 'right'
    elif main_type in (25, 35):
        if direction_type == 64:
            return 'left'
        if direction_type == 192:
            return 'right'
    elif main_type in (16, 27):
        return 'left'
    elif main_type in (15, 26):
        return 'right'
    return None


def read_auth(path):
    if not path:
        return None
    with codecs.open(path, 'r', encoding='utf8') as source:
        value = source.read().strip()
    if not value or any(c in value for c in ('\r', '\n', '\0')):
        raise Failure('Authorization file must contain one nonempty header value')
    return value


def reference_uri(client, reference, resource):
    if isinstance(reference, dict):
        reference = reference.get('uri')
    return client.checked(reference, resource)


class NavigationSource(object):
    """Acquire a fresh full snapshot; follow only the current CarPlay reference.

    No WS frames are guessed. GET polling uses the upstream envelope/pagination
    parser and explicit source service base. URI and authority checks are shared.
    """
    def __init__(self, client):
        self.client = client

    def poll(self):
        contexts = self.client.collection('drivingContexts')
        candidates = [row for row in contexts
                      if row.get('technology') == 'appleCarPlay' and
                      row.get('isRouteGuidanceActive') is True]
        if len(candidates) > 1:
            raise Failure('Multiple active CarPlay contexts; source is ambiguous')
        if not candidates:
            return None
        context = candidates[0]
        context_uri = self.client.element_uri(context, 'drivingContexts')
        ref = context.get('nextManeuver')
        if not ref:
            return None
        maneuver_uri = reference_uri(self.client, ref, 'maneuvers')
        rows = light.items(self.client.request('GET', maneuver_uri)[1])
        if len(rows) != 1:
            raise Failure('Expected exactly one current maneuver element')
        maneuver = rows[0]
        if self.client.element_uri(maneuver, 'maneuvers') != maneuver_uri:
            raise Failure('Current maneuver URI changed during read')
        if not isinstance(maneuver.get('id'), STRING) or not maneuver['id']:
            raise Failure('Current maneuver requires an id')
        stage = maneuver.get('stage')
        if stage not in ('follow', 'prepare', 'instruction', 'execute'):
            raise Failure('Expected a simplenavigation stage string')
        direction = turn_direction(maneuver.get('mainType'), maneuver.get('directionType'))
        # Re-read the context after the element: do not play an old reference
        # if a stop/re-route occurs between the two HTTP reads.
        confirm = light.items(self.client.request('GET', context_uri)[1])
        if len(confirm) != 1:
            raise Failure('CarPlay context disappeared during snapshot')
        current = confirm[0]
        if (current.get('technology') != 'appleCarPlay' or
                current.get('isRouteGuidanceActive') is not True or
                self.client.element_uri(current, 'drivingContexts') != context_uri or
                reference_uri(self.client, current.get('nextManeuver'), 'maneuvers') != maneuver_uri):
            raise SnapshotChanged()
        return {'key': (context_uri, maneuver_uri, maneuver['id'],
                        maneuver.get('mainType'), maneuver.get('directionType')),
                'direction': direction, 'stage': stage,
                'mainType': maneuver.get('mainType'),
                'directionType': maneuver.get('directionType'),
                'distance': maneuver.get('distance')}

    def still_current(self, key):
        """Revalidate after light preflight, before any queue mutation."""
        contexts = light.items(self.client.request('GET', key[0])[1])
        if len(contexts) != 1:
            return False
        row = contexts[0]
        if (row.get('technology') != 'appleCarPlay' or
                row.get('isRouteGuidanceActive') is not True or
                reference_uri(self.client, row.get('nextManeuver'), 'maneuvers') != key[1]):
            return False
        rows = light.items(self.client.request('GET', key[1])[1])
        return (len(rows) == 1 and rows[0].get('id') == key[2] and
                rows[0].get('stage') in ('execute', 'follow') and
                rows[0].get('mainType') == key[3] and
                rows[0].get('directionType') == key[4])


class PulseSink(object):
    """Reuse upstream request shape, Location identity, preflight and rollback."""
    def __init__(self, client, state_dir, apply=False):
        self.client, self.state_dir, self.apply = client, state_dir, apply
        self.state = None
        self.path = None
        self.before_post = None
        self.lock_path = None
        self._prepare_dir()

    def _prepare_dir(self):
        if not os.path.isdir(self.state_dir):
            os.mkdir(self.state_dir, 0o700)
        if os.path.islink(self.state_dir) or not os.path.isdir(self.state_dir):
            raise Failure('State directory must be an existing real directory')
        if self.apply:
            lock = os.path.join(self.state_dir, 'carplay-monitor.lock')
            try:
                os.mkdir(lock, 0o700)
            except OSError:
                raise Failure('Monitor lock exists; stop the other instance or inspect stale lock')
            self.lock_path = lock
            try:
                for name in os.listdir(self.state_dir):
                    if name.startswith('carplay-') and name.endswith('.json'):
                        with codecs.open(os.path.join(self.state_dir, name), 'r', encoding='utf8') as source:
                            state = json.load(source)
                        if state.get('phase') != 'cleaned':
                            raise Failure('Pending request state exists; run rollback.sh --state first')
            except BaseException:
                self.release_lock()
                raise

    def release_lock(self):
        if self.lock_path is not None:
            os.rmdir(self.lock_path)
            self.lock_path = None

    def active(self):
        return self.state is not None

    def start(self, direction):
        if self.active():
            raise Failure('Previous request requires cleanup before a new pulse')
        args = light.Options()
        args.direction, args.purpose, args.purpose_uri = direction, None, None
        purpose = light.select_purpose(self.client, args)
        purpose_uri = self.client.element_uri(purpose, 'purposes')
        light.preflight(self.client, purpose)
        # Re-check the navigation master group if the server exposes it. Some
        # installations expose nav_turn only; purpose-specific checks above apply.
        groups = self.client.collection('settinggroups')
        if any(g.get('name') in ('navigation', 'nav_turn') and
               g.get('isActivated') is False for g in groups):
            light.emit('carplay_skipped', reason='navigation_group_off')
            return False
        baseline = self.client.collection('queuedpurposes')
        name = purpose.get('name')
        if not isinstance(name, STRING) or not name:
            raise Failure('Real purpose name is required')
        if any(row.get('name') == name for row in baseline):
            light.emit('carplay_skipped', reason='purpose_already_queued')
            return False
        light.emit('carplay_plan', direction=direction, apply=self.apply,
                   purpose_name=name, physical_light_verified=False)
        if not self.apply:
            light.emit('dry_run', mutations=0)
            return True
        if self.before_post is not None and not self.before_post():
            light.emit('carplay_skipped', reason='navigation_changed_during_preflight')
            return False
        uris = [self.client.element_uri(row, 'queuedpurposes') for row in baseline]
        self.path = os.path.join(self.state_dir, 'carplay-' + random_hex() + '.json')
        self.state = {'kind': 'smartlight-short-request-v2', 'phase': 'creating',
                      'base': self.client.base, 'advertised_base': self.client.advertised_base,
                      'purpose_uri': purpose_uri, 'request_name': name,
                      'baseline_queue_uris': uris, 'queue_uri': None,
                      'created_time': time.time(), 'run_id': random_hex(), 'direction': direction}
        light.write_state(self.path, self.state, exclusive=True)
        light.emit('state_saved', state=self.path)
        pending = []
        previous = {}
        def defer(signum, frame):
            pending.append(signum)
        def record_response(code, location):
            self.state['http_status'] = code
            if location:
                uri = self.client.checked(location, 'queuedpurposes')
                if uri in uris:
                    raise Failure('POST Location overlaps a pre-existing request')
                self.state['queue_uri'] = uri
            if 200 <= code < 300:
                self.state['phase'] = 'posted'
            elif code in (400, 401, 403, 404, 405, 415, 422) and not location:
                self.state['phase'] = 'rejected'
            light.write_state(self.path, self.state)
        try:
            for sig in (signal.SIGINT, signal.SIGTERM):
                previous[sig] = signal.signal(sig, defer)
            self.client.request('POST', self.client.resource('queuedpurposes'),
                                {'name': name, 'purpose': light.urlsplit(purpose_uri).path},
                                response_hook=record_response)
        finally:
            for sig, handler in previous.items():
                signal.signal(sig, handler)
        if pending:
            raise light.Interrupted('Signal during bounded POST')
        if not self.state['queue_uri']:
            raise Failure('POST did not return Location; retain state and stop')
        return True

    def stop(self):
        if self.state is None:
            return
        previous = {}
        try:
            for sig in (signal.SIGINT, signal.SIGTERM):
                previous[sig] = signal.signal(sig, signal.SIG_IGN)
            light.cleanup(self.client, self.state, self.path)
        finally:
            for sig, handler in previous.items():
                signal.signal(sig, handler)
        self.state = None
        self.path = None


class Controller(object):
    """One bounded pulse per maneuver; no indefinite queue entry or replay."""
    def __init__(self, sink, hold=2.0, max_duration=30.0, clock=None):
        self.sink, self.hold, self.max_duration = sink, hold, max_duration
        self.clock = clock or getattr(time, 'monotonic', time.time)
        self.key, self.started, self.until = None, None, None
        self.fired = False
        self.last_time = None
        self.retired = set()
        self.retired_limit = 4096

    def _retire(self):
        if self.key:
            if len(self.retired) >= self.retired_limit:
                raise Failure('Maneuver history limit reached; restart with a fresh session')
            self.retired.add(self.key)

    def step(self, event):
        now = self.clock()
        if self.last_time is not None and now < self.last_time:
            self.sink.stop()
            raise Failure('Clock moved backwards; stop and retain cleanup evidence')
        self.last_time = now
        if self.until is not None and now >= self.until:
            self.sink.stop()
            self.until = None
        if event is None:
            self.sink.stop()
            self.key, self.started, self.until = None, None, None
            self.fired = False
            # A confirmed inactive source ends the navigation session. Resource
            # UUIDs and iAP2 maneuver indexes may be reused on the next trip.
            self.retired.clear()
            return
        key = tuple(event['key'])
        if key in self.retired:
            self.sink.stop()
            return
        if key != self.key:
            self.sink.stop()
            self._retire()
            self.key, self.started, self.fired = key, None, False
            self.until = None
        if self.started is not None and now - self.started > self.max_duration:
            self.sink.stop()
            self.fired = True
            return
        direction = event.get('direction')
        stage = event.get('stage')
        if direction not in ('left', 'right'):
            self.sink.stop()
            self.until = None
            return
        if stage == 'execute':
            # 原厂/内置导航语义：进入执行阶段即触发，不看距离。
            pass
        elif stage == 'follow':
            # CarPlay 只会停留在 follow；用距离近似“该转弯了”。
            dist = event.get('distance')
            if not (isinstance(dist, (int, float)) and dist <= 150):
                self.sink.stop()
                self.until = None
                return
        else:
            self.sink.stop()
            self.until = None
            return
        # 持续型：hold 到期后若仍在接近同一机动，则重新打一次，
        # 直到离开 near 或累计超过 max_duration（由上方检查兜底）。
        if self.fired and self.until is None and self.started is not None:
            if now - self.started <= self.max_duration:
                self.fired = False
        if not self.fired:
            # Mark before mutation: a failed/unknown POST must not be retried.
            self.fired = True
            if self.started is None:
                self.started = now
            if self.sink.start(direction):
                self.until = self.clock() + self.hold

    def close(self):
        self.sink.stop()

    def expire(self):
        self.sink.stop()
        self.until = None


class CommandLine(object):
    HELP = '''Usage: carplay_monitor.py --nav-base URI --base URI [options]

  --nav-base URI              Existing simplenavigation service base
  --nav-registry URI          VIWI service registry (e.g. http://[::1]:80); when
                              given, simplenavigation is resolved from it and
                              --nav-base is not required
  --base URI                  Existing SmartLight service base
  --nav-advertised-base URI    Explicit navigation URI prefix mapping
  --advertised-base URI        Explicit SmartLight URI prefix mapping
  --nav-auth-file PATH         Navigation Authorization value (never inherited)
  --auth-file PATH             SmartLight Authorization value
  --interval SECONDS           Poll delay, 0.1..5 (default 0.5)
  --timeout SECONDS            Each HTTP request, 0.1..15 (default 2)
  --hold SECONDS               One pulse per maneuver, 0.1..5 (default 2)
  --max-duration SECONDS       Max execute observation, 1..300 (default 30)
  --state-dir PATH             Private new/pending-free state directory
  --iterations N              Stop after N polls (default 0: continuous)
  --apply                     Create requests; default is read-only polling
  -h, --help                  Show help
'''
    def error(self, message):
        sys.stderr.write(message + '\n' + self.HELP)
        raise SystemExit(2)

    def parse_args(self, argv):
        result = light.Options()
        values = {'nav_base': None, 'nav_registry': None, 'base': None,
                  'nav_advertised_base': None,
                  'advertised_base': None, 'nav_auth_file': None, 'auth_file': None,
                  'interval': 0.5, 'timeout': 2.0, 'hold': 2.0,
                  'max_duration': 30.0, 'state_dir': None, 'iterations': 0, 'apply': False}
        names = dict(('--' + name.replace('_', '-'), name) for name in values if name != 'apply')
        i = 0
        while i < len(argv):
            token = argv[i]
            if token in ('-h', '--help'):
                print(self.HELP)
                raise SystemExit(0)
            if token == '--apply':
                values['apply'] = True
            elif token in names:
                i += 1
                if i >= len(argv) or argv[i].startswith('--'):
                    self.error('Missing value for ' + token)
                key = names[token]
                value = argv[i]
                try:
                    if key in ('interval', 'timeout', 'hold', 'max_duration'):
                        value = float(value)
                    elif key == 'iterations':
                        value = int(value)
                except ValueError:
                    self.error('Invalid numeric option ' + token)
                values[key] = value
            else:
                self.error('Unknown option ' + token)
            i += 1
        if not values['base'] or not (values['nav_base'] or values['nav_registry']):
            self.error('--base and either --nav-base or --nav-registry are required')
        for name, low, high in [('interval', 0.1, 5), ('timeout', 0.1, 15),
                                ('hold', 0.1, 5), ('max_duration', 1, 300)]:
            if not low <= values[name] <= high:
                self.error(name + ' is outside its supported range')
        if values['iterations'] < 0:
            self.error('iterations must be nonnegative')
        for key, value in values.items():
            setattr(result, key, value)
        return result


def main(argv=None):
    args = CommandLine().parse_args(sys.argv[1:] if argv is None else argv)
    def interrupted(signum, frame):
        raise light.Interrupted('Signal %d' % signum)
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, interrupted)
    sink, controller = None, None
    status = 0
    try:
        if args.nav_registry:
            args.nav_base = light.resolve_service(
                args.nav_registry, 'simplenavigation', args.timeout)
        nav = light.Client(args.nav_base, read_auth(args.nav_auth_file), args.timeout,
                           args.nav_advertised_base)
        lamps = light.Client(args.base, read_auth(args.auth_file), args.timeout,
                             args.advertised_base)
        state_dir = args.state_dir or os.path.join(temporary_parent(), 'smartlight-carplay')
        sink = PulseSink(lamps, state_dir, args.apply)
        controller = Controller(sink, args.hold, args.max_duration)
        source = NavigationSource(nav)
        sink.before_post = lambda: source.still_current(controller.key)
        light.emit('carplay_started', apply=args.apply, state_dir=state_dir,
                   input='simplenavigation_http_poll', physical_light_verified=False)
        count = 0
        while not args.iterations or count < args.iterations:
            # A slow navigation GET must not extend a playback request for all
            # three request timeouts. Linux setitimer interrupts the parent;
            # run_curl reaps its child before the bounded cleanup.
            timer = False
            old_alarm = None
            if controller.until is not None and sink.active():
                remaining = controller.until - controller.clock()
                if remaining <= 0:
                    controller.expire()
                elif hasattr(signal, 'setitimer'):
                    def expired(signum, frame):
                        raise PulseExpired()
                    old_alarm = signal.signal(signal.SIGALRM, expired)
                    signal.setitimer(signal.ITIMER_REAL, remaining)
                    timer = True
            try:
                event = source.poll()
            except SnapshotChanged:
                if timer:
                    signal.setitimer(signal.ITIMER_REAL, 0)
                    signal.signal(signal.SIGALRM, old_alarm)
                    timer = False
                controller.expire()
                count += 1
                continue
            except PulseExpired:
                # Cancel before DELETE; a second alarm must not abort rollback.
                signal.setitimer(signal.ITIMER_REAL, 0)
                timer = False
                signal.signal(signal.SIGALRM, old_alarm)
                controller.expire()
                count += 1
                continue
            finally:
                if timer:
                    signal.setitimer(signal.ITIMER_REAL, 0)
                    signal.signal(signal.SIGALRM, old_alarm)
            controller.step(event)
            count += 1
            light.emit('carplay_snapshot', active=event is not None,
                       stage=event.get('stage') if event else None,
                       direction=event.get('direction') if event else None)
            if not args.iterations or count < args.iterations:
                pause = args.interval
                if controller.until is not None and sink.active():
                    pause = min(pause, max(0.0, controller.until - controller.clock()))
                time.sleep(pause)
    except (light.Interrupted, KeyboardInterrupt):
        status = 130
    except (Failure, ValueError, KeyError, TypeError, IOError, OSError) as exc:
        light.emit('error', error=str(exc))
        status = 1
    finally:
        if controller:
            try:
                controller.close()
            except (Failure, ValueError, IOError, OSError) as exc:
                light.emit('cleanup_pending', state=sink.path, error=str(exc))
                status = 1
        if sink:
            try:
                sink.release_lock()
            except OSError as exc:
                light.emit('error', error='State lock release failed: ' + str(exc))
                status = 1
    return status


if __name__ == '__main__':
    sys.exit(main())
