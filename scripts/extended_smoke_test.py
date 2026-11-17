#!/usr/bin/env python3
"""Exercise Extended features against the real Compose TCP simulators."""
import time
from urllib.request import Request, urlopen
from smoke_test import login, request

def admin(port, path):
    with urlopen(Request(f'http://localhost:{port}{path}', method='POST'), timeout=8) as response:
        assert response.status == 200

def wait_for(check, timeout=45):
    deadline=time.monotonic()+timeout
    while time.monotonic()<deadline:
        if check(): return
        time.sleep(0.5)
    raise AssertionError('Timed out waiting for verified device state')

def main():
    operator=login('OPERATOR','operator');viewer=login('VIEWER','viewer')
    code,snapshot=request('/api/config/control',token=operator);assert code==200
    original=snapshot['config']
    try:
        wait_for(lambda: request('/api/thermocouple',token=operator)[1]['channels'][0]['status']=='live')
        admin(5123,'/channel-fault?ch=1&broken=true')
        def independent_fault():
            code,data=request('/api/thermocouple',token=operator)
            return code==200 and data['channels'][1]['status']=='fault' and data['channels'][1]['temperature_c'] is None and all(data['channels'][i]['status']=='live' for i in [0,2,3])
        wait_for(independent_fault)
        for ch in [0,2,3]:admin(5123,f'/channel-fault?ch={ch}&broken=true')
        wait_for(lambda:request('/api/thermocouple',token=operator)[1]['all_channels_faulted'])
        for ch in range(4):admin(5123,f'/channel-fault?ch={ch}&broken=false')
        wait_for(lambda:all(ch['status']=='live' for ch in request('/api/thermocouple',token=operator)[1]['channels']))
        print('[OK] independent channels, all-channel fault, recovery')
        auto={**original,'enabled':True,'on_above':30,'off_below':27}
        assert request('/api/config/control','PUT',operator,auto)[0]==200
        admin(5121,'/temp?value=35')
        wait_for(lambda:request('/api/fan/speed',token=operator)[1].get('command_pct')==100)
        assert request('/api/fan/speed','POST',operator,{'percent':50})[0]==409
        admin(5124,'/feedback?rpm=0')
        wait_for(lambda:request('/api/fan/speed',token=operator)[1].get('mismatch') is True)
        admin(5124,'/feedback?rpm=auto')
        admin(5121,'/temp?value=20')
        wait_for(lambda:request('/api/fan/speed',token=operator)[1].get('command_pct')==0)
        print('[OK] AUTO curve, hysteresis OFF coordination, 409, sustained mismatch')
        assert request('/api/config/control','PUT',operator,{**auto,'enabled':False})[0]==200
        assert request('/api/fan/speed','POST',viewer,{'percent':50})[0]==403
        assert request('/api/fan/speed','POST',operator,{'percent':50})[0]==200
        assert request('/api/fan/speed',token=operator)[1]['command_pct']==50
        print('[OK] MANUAL read-back and viewer speed rejection')
        for target,name in [('thermocouple','thermocouple'),('speed-pwm','fan_speed'),('relay','relay'),('temp-humidity','sensor')]:
            assert request('/api/reset','POST',viewer,{'target':target})[0]==403
            assert request('/api/reset','POST',operator,{'target':target})[0]==202
            states=request('/api/devices',token=operator)[1]
            assert next(d for d in states if d['name']==name)['state']!='online'
            if target=='thermocouple':assert all(ch['temperature_c'] is None for ch in request('/api/thermocouple',token=operator)[1]['channels'])
            wait_for(lambda:any(d['name']==name and d['state']=='online' for d in request('/api/devices',token=operator)[1]))
            print(f'[OK] {target}: viewer 403, TCP reset, invalidation, fresh recovery')
        events=request('/api/events?limit=1000',token=operator)[1]
        resets=[ev for ev in events if ev['kind']=='device_reset' and ev['message']=='initiated']
        assert all(any(ev['source']==target and ev['actor'] for ev in resets) for target in ['relay','thermocouple','speed-pwm','temp-humidity'])
        print('[OK] named reset audit events; backend health:',request('/healthz')[0])
    finally:
        for ch in range(4):admin(5123,f'/channel-fault?ch={ch}&broken=false')
        admin(5124,'/feedback?rpm=auto');admin(5121,'/temp?value=auto')
        assert request('/api/config/control','PUT',operator,original)[0]==200
if __name__=='__main__':main()
