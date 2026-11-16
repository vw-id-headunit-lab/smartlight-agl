"""Local-only CarPlay polling/queue integration; never targets a headunit."""
from pathlib import Path
import copy
import json
import os
import signal
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler
from urllib.parse import urlsplit, parse_qs

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(ROOT/'tests'))
import smartlight_trigger as light
import carplay_monitor as cp
import test_trigger as upstream

NAV = {}
NAV_LOCK = threading.Lock()
RUNS = []
MUTATION_TIMES = []

def reset_nav(**kwargs):
    with NAV_LOCK:
        NAV.clear()
        NAV.update({'polls':0, 'stage':'execute', 'direction':64, 'main':13,
                    'active':True, 'change_at':None, 'stop_at':None,
                    'invalid_ref':False, 'delay_at':None, 'delay':0,
                    'omit_field':None, 'credential':None, 'requests':[],
                    'id':'current', 'technology':'appleCarPlay',
                    'context_reads':0, 'stop_on_recheck':False,
                    'maneuver_reads':0, 'churn_after':None})
        NAV.update(kwargs)

class Handler(upstream.Handler):
    def send(self, code, payload=None, location=None, raw=None):
        if self.command in ('POST','DELETE'):
            MUTATION_TIMES.append((self.command,time.monotonic()))
        try:
            return super().send(code,payload,location,raw)
        except (BrokenPipeError,ConnectionResetError):
            # A bounded source GET is intentionally interrupted by pulse expiry.
            return

    def handle_request(self):
        if self.path.startswith('/simplenavigation/'):
            with NAV_LOCK:
                NAV['requests'].append({'path':self.path, 'method':self.command})
                f=dict(NAV)
                if self.path.endswith('/drivingContexts/'):
                    NAV['polls']+=1
                    f['polls']=NAV['polls']
                elif '/maneuvers/' in self.path:
                    # CarPlay 会给同一个转弯换新 element id（方向/距离不变）
                    NAV['maneuver_reads']+=1
                    if f['churn_after'] and NAV['maneuver_reads']>=f['churn_after']:
                        f['id']='churned'
                elif '/drivingContexts/' in self.path:
                    NAV['context_reads']+=1
                    if f['stop_on_recheck'] and NAV['context_reads']>=2:
                        f['active']=False
                if f['stop_at'] and f['polls']>=f['stop_at']:
                    f['active']=False
                if f['change_at'] and f['polls']>=f['change_at']:
                    f['direction']=192
                    f['id']='changed'
            if f['credential'] and self.headers.get('Authorization')!=f['credential']:
                self.send(401);return
            if f['delay_at'] and f['polls']>=f['delay_at']:
                time.sleep(f['delay'])
            if self.command!='GET':
                self.send(405);return
            if '/drivingContexts/' in self.path:
                row={'id':'cp','uri':'/simplenavigation/drivingContexts/cp',
                     'name':'phone','technology':f['technology'],
                     'isRouteGuidanceActive':f['active'],
                     'nextManeuver':{'uri':('http://127.0.0.1:1/other' if f['invalid_ref']
                                            else '/simplenavigation/maneuvers/'+f['id'])}}
                if f['omit_field']:row.pop(f['omit_field'],None)
                self.send(200,[row]);return
            if '/maneuvers/' in self.path:
                element=self.path.rstrip('/').split('/')[-1]
                self.send(200,[{'id':element,'uri':'/simplenavigation/maneuvers/'+element,
                               'mainType':f['main'],'directionType':f['direction'],
                               'stage':f['stage'],'distance':50,'distanceUnit':'m'}]);return
            self.send(404);return
        return super().handle_request()
    do_GET=handle_request
    do_POST=handle_request
    do_DELETE=handle_request

class FakeSink:
    def __init__(self):self.calls=[]
    def start(self,direction):self.calls.append(('start',direction));return True
    def stop(self):self.calls.append(('stop',))

class DirectionAndController(unittest.TestCase):
    def test_native_rsi_direction_families(self):
        for direction in (32,64,96):self.assertEqual('left',cp.turn_direction(13,direction))
        for direction in (160,192,224):self.assertEqual('right',cp.turn_direction(13,direction))
        self.assertEqual('left',cp.turn_direction(25,64))
        self.assertEqual('right',cp.turn_direction(35,192))
        self.assertEqual('left',cp.turn_direction(27,255))
        self.assertEqual('right',cp.turn_direction(15,255))
        for m,d in [(13,0),(13,255),(1,64),(True,64),(13,'64')]:
            self.assertIsNone(cp.turn_direction(m,d))

    def test_pulse_repeats_while_near_stops_on_leave_and_new_maneuver(self):
        # 内置导航会在转弯过程中每 2-4 秒重发一条 queuedpurpose（实车抓取），
        # 因此 hold 到期后只要仍在接近同一机动，就应再打一次。
        now=[0];sink=FakeSink();c=cp.Controller(sink,0.5,30,lambda:now[0])
        event={'key':('c','m','1'),'direction':'left','stage':'execute'}
        c.step(event)
        self.assertEqual(1,len([x for x in sink.calls if x[0]=='start']))
        now[0]=0.2;c.step(event)
        self.assertEqual(1,len([x for x in sink.calls if x[0]=='start']))
        now[0]=0.6;c.step(event)
        self.assertEqual(2,len([x for x in sink.calls if x[0]=='start']))
        c.step(None);now[0]=1;c.step(event)
        self.assertEqual(3,len([x for x in sink.calls if x[0]=='start']))
        c.step(dict(event,key=('c','new','2'),direction='right'))
        self.assertEqual(4,len([x for x in sink.calls if x[0]=='start']))
        # 超过 max_duration 后不再重发
        now[0]=100;c.step(dict(event,key=('c','new','2'),direction='right'))
        self.assertEqual(4,len([x for x in sink.calls if x[0]=='start']))

    def test_long_follow_before_execute_still_gets_one_pulse(self):
        now=[0];sink=FakeSink();c=cp.Controller(sink,clock=lambda:now[0])
        e={'key':('c','m','1'),'direction':'left','stage':'prepare'}
        c.step(e);now[0]=31;c.step(dict(e,stage='execute'))
        self.assertEqual(1,len([x for x in sink.calls if x[0]=='start']))

    def test_retired_maneuver_not_replayed_within_active_session(self):
        sink=FakeSink();c=cp.Controller(sink)
        a={'key':('c','a','1'),'direction':'left','stage':'execute'}
        b=dict(a,key=('c','b','2'),direction='right')
        c.step(a);c.step(b);c.step(a)
        self.assertEqual(2,len([x for x in sink.calls if x[0]=='start']))

    def test_clock_backwards_stops(self):
        now=[10];sink=FakeSink();c=cp.Controller(sink,clock=lambda:now[0]);c.step(None)
        now[0]=9
        with self.assertRaises(light.Failure):c.step(None)

class RegistryHandler(BaseHTTPRequestHandler):
    """Mock VIWI service registry: only ?name= is answered, everything else 307."""
    services = {}
    def log_message(self, *a): pass
    def do_GET(self):
        parsed = urlsplit(self.path)
        name = parse_qs(parsed.query).get('name', [None])[0]
        if not name:
            # 与真机一致：除 ?name= 之外的路径一律重定向
            self.send_response(307); self.send_header('Location','/'); self.end_headers(); return
        uri = self.services.get(name)
        rows = [{'name':name,'uri':uri}] if uri else []
        body = json.dumps({'data':rows}).encode()
        self.send_response(200); self.send_header('Content-Type','application/json')
        self.send_header('Content-Length',str(len(body))); self.end_headers(); self.wfile.write(body)


class Integration(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server=ThreadingHTTPServer(('127.0.0.1',0),Handler)
        cls.thread=threading.Thread(target=cls.server.serve_forever,daemon=True);cls.thread.start()
        cls.origin='http://127.0.0.1:%d'%cls.server.server_port
        RegistryHandler.services={'simplenavigation': cls.origin+'/simplenavigation/'}
        cls.registry=ThreadingHTTPServer(('127.0.0.1',0),RegistryHandler)
        cls.registry_thread=threading.Thread(target=cls.registry.serve_forever,daemon=True)
        cls.registry_thread.start()
        cls.registry_origin='http://127.0.0.1:%d'%cls.registry.server_port

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown();cls.server.server_close();cls.thread.join()
        cls.registry.shutdown();cls.registry.server_close();cls.registry_thread.join()
        out=ROOT/'artifacts';out.mkdir(exist_ok=True)
        (out/'carplay-verification.json').write_text(json.dumps({'kind':'loopback-mock-not-vehicle',
               'runs':RUNS,'runtime':sys.version,'vehicle_tested':False},ensure_ascii=False,indent=2)+'\n',encoding='utf8')

    def setUp(self):
        self.baseline=upstream.reset();reset_nav()
        MUTATION_TIMES[:]=[]
        self.temp=tempfile.TemporaryDirectory(prefix='smartlight-carplay-test-')
        self.dir=Path(self.temp.name)

    def tearDown(self):self.temp.cleanup()

    def run_monitor(self, options=(), expected=0, interrupted=False, nav_args=None):
        nav = nav_args if nav_args is not None else ['--nav-base', self.origin+'/simplenavigation/']
        cmd=[sys.executable,str(ROOT/'carplay_monitor.py')]+nav+[
             '--base',self.origin+'/smartlight/','--interval','0.1','--hold','0.3','--timeout','1',
             '--state-dir',str(self.dir/'states'),'--iterations','5']+list(options)
        if interrupted:
            p=subprocess.Popen(cmd,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
            for _ in range(200):
                if upstream.snapshot()['writes']:break
                time.sleep(0.01)
            p.send_signal(signal.SIGTERM);out,err=p.communicate(timeout=10)
            result=subprocess.CompletedProcess(cmd,p.returncode,out,err)
        else:result=subprocess.run(cmd,capture_output=True,text=True,timeout=20)
        fixture=upstream.snapshot()
        RUNS.append({'argv':cmd,'exit_code':result.returncode,'stdout':result.stdout,'stderr':result.stderr,
                     'http_requests':fixture['requests'],'mutations':fixture['writes'],
                     'queue_after':fixture['queue'],'baseline_preserved':fixture['queue']==self.baseline,
                     'nav_requests':list(NAV['requests'])})
        self.assertEqual(expected,result.returncode,(result.stdout,result.stderr))
        return result,fixture

    def test_nav_registry_resolves_service_without_nav_base(self):
        # 用户不该靠猜端口：给出注册中心即可，服务地址由其回答解析。
        result,f=self.run_monitor(['--iterations','3'],
                                  nav_args=['--nav-registry',self.registry_origin])
        self.assertFalse(f['writes'])
        self.assertIn('carplay_snapshot',result.stdout)

    def test_unknown_service_on_registry_fails_closed(self):
        RegistryHandler.services={}
        try:
            result,f=self.run_monitor(['--iterations','1'],
                                      expected=1,
                                      nav_args=['--nav-registry',self.registry_origin])
        finally:
            RegistryHandler.services={'simplenavigation': self.origin+'/simplenavigation/'}
        self.assertIn('not registered',result.stdout+result.stderr)

    def test_dry_run_reads_current_reference_no_mutations(self):
        result,f=self.run_monitor()
        self.assertFalse(f['writes']);self.assertEqual(self.baseline,f['queue'])
        self.assertIn('"direction": "left"',result.stdout)
        self.assertTrue(any('/maneuvers/current' in r['path'] for r in NAV['requests']))

    def test_apply_real_name_relative_uri_location_and_rollback(self):
        result,f=self.run_monitor(['--apply'])
        # 持续模式下可能有多轮 POST/DELETE；每轮都必须成对、body 一致。
        methods=[x['method'] for x in f['writes']]
        self.assertTrue(methods, 'expected at least one mutation')
        self.assertEqual('POST', methods[0])
        self.assertEqual(0, len(methods) % 2, methods)
        for i in range(0, len(methods), 2):
            self.assertEqual(['POST','DELETE'], methods[i:i+2])
        self.assertEqual({'name':'15','purpose':'/smartlight/purposes/left-id'},f['writes'][0]['body'])
        self.assertEqual(self.baseline,f['queue'])
        state=json.loads(next((self.dir/'states').glob('carplay-*.json')).read_text())
        self.assertEqual('cleaned',state['phase'])
        rollback=[sys.executable,str(ROOT/'smartlight_trigger.py'),'cleanup','--state',
                  str(next((self.dir/'states').glob('carplay-*.json')))]
        r=subprocess.run(rollback,capture_output=True,text=True)
        self.assertEqual(0,r.returncode,r.stdout)

    def test_right_mapping_and_change_clear_old_first(self):
        reset_nav(change_at=3)
        result,f=self.run_monitor(['--apply'])
        self.assertEqual(['POST','DELETE','POST','DELETE'],[x['method'] for x in f['writes']])
        self.assertEqual('14',f['writes'][2]['body']['name'])
        self.assertEqual(self.baseline,f['queue'])

    def test_stop_clears_immediately(self):
        reset_nav(stop_at=2)
        result,f=self.run_monitor(['--apply'])
        self.assertEqual(['POST','DELETE'],[x['method'] for x in f['writes']])
        self.assertEqual(self.baseline,f['queue'])

    def test_maneuver_id_churn_still_signals_the_same_turn(self):
        # CarPlay 行车中会刷新机动元素；同一个转弯换了 id 不应被 preflight 拒掉。
        reset_nav(stage='follow', churn_after=2)
        result,f=self.run_monitor(['--apply','--iterations','4'])
        self.assertTrue(f['writes'], 'same-direction churn must still signal')
        self.assertEqual('15', f['writes'][0]['body']['name'])
        self.assertEqual(self.baseline,f['queue'])
        self.assertNotIn('navigation_changed_during_preflight',result.stdout)

    def test_stop_between_preflight_and_post_does_not_mutate(self):
        reset_nav(stop_on_recheck=True)
        result,f=self.run_monitor(['--apply','--iterations','1'])
        self.assertFalse(f['writes'])
        self.assertIn('navigation_changed_during_preflight',result.stdout)

    def test_non_execute_unknown_and_non_carplay_do_not_write(self):
        for options in [{'stage':'prepare'},{'direction':0},{'technology':'androidAuto'}]:
            reset_nav(**options);upstream.reset()
            self.run_monitor(['--apply','--iterations','1'])
            self.assertFalse(upstream.snapshot()['writes'])

    def test_cross_origin_navigation_ref_rejected(self):
        reset_nav(invalid_ref=True)
        self.run_monitor(['--apply'],expected=1)
        self.assertFalse(upstream.snapshot()['writes'])

    def test_separate_navigation_credentials(self):
        token='Bearer fixture-navigation-only';reset_nav(credential=token)
        file=self.dir/'nav-header.txt';file.write_text(token)
        result,f=self.run_monitor(['--nav-auth-file',str(file),'--iterations','1'])
        self.assertFalse(f['writes']);self.assertNotIn(token,result.stdout+result.stderr)

    def test_disabled_group_no_write(self):
        upstream.reset(group_off=True)
        self.run_monitor(['--apply'],expected=1)
        self.assertFalse(upstream.snapshot()['writes'])

    def test_same_name_existing_request_preserved(self):
        with upstream.LOCK:
            upstream.FIXTURE['queue'].append({'id':'same','uri':'/smartlight/queuedpurposes/same','name':'15'})
        result,f=self.run_monitor(['--apply'])
        self.assertFalse(f['writes']);self.assertEqual(2,len(f['queue']))

    def test_existing_monitor_lock_blocks_duplicate_apply(self):
        states=self.dir/'states';states.mkdir();(states/'carplay-monitor.lock').mkdir()
        result,f=self.run_monitor(['--apply'],expected=1)
        self.assertFalse(f['writes'])
        self.assertTrue((states/'carplay-monitor.lock').is_dir())

    def test_missing_location_stops_and_retains_recovery_state(self):
        upstream.reset(no_location=True)
        result,f=self.run_monitor(['--apply'],expected=1)
        self.assertEqual(['POST'],[x['method'] for x in f['writes']])
        self.assertIn('cleanup_pending',result.stdout)
        state=json.loads(next((self.dir/'states').glob('carplay-*.json')).read_text())
        self.assertNotEqual('cleaned',state['phase'])
        self.run_monitor(['--apply'],expected=1)
        self.assertEqual(1,len(upstream.snapshot()['writes']))

    def test_error_after_create_still_cleans_only_own_location(self):
        upstream.reset(post_error=True)
        result,f=self.run_monitor(['--apply'],expected=1)
        self.assertEqual(['POST','DELETE'],[x['method'] for x in f['writes']])
        self.assertEqual(self.baseline,f['queue'])

    def test_invalid_post_json_with_location_can_be_cleaned(self):
        upstream.reset(non_json_create=True)
        result,f=self.run_monitor(['--apply'],expected=1)
        self.assertEqual(['POST','DELETE'],[x['method'] for x in f['writes']])
        self.assertEqual(self.baseline,f['queue'])

    def test_sigterm_cleans(self):
        result,f=self.run_monitor(['--apply','--hold','5','--iterations','0'],expected=130,interrupted=True)
        self.assertEqual(self.baseline,f['queue'])

    @unittest.skipUnless(hasattr(signal,'setitimer'),'requires POSIX timer')
    def test_slow_navigation_read_does_not_extend_pulse(self):
        reset_nav(delay_at=2,delay=2)
        result,f=self.run_monitor(['--apply','--iterations','2','--timeout','3'])
        self.assertEqual(self.baseline,f['queue'])
        self.assertEqual(['POST','DELETE'],[x['method'] for x in f['writes']])
        self.assertLess(MUTATION_TIMES[1][1]-MUTATION_TIMES[0][1],1.2)

if __name__=='__main__':unittest.main(verbosity=2)
