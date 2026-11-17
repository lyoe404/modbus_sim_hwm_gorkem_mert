#!/usr/bin/env python3
"""Opt-in destructive Core integration test against running Docker stack.

Pins the simulator temperature and writes the AUTO control configuration;
restores both in finally. Run only on local test environment, not production.
"""
import json
import os
import time
from urllib.request import Request, urlopen
from urllib.error import HTTPError
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
for line in (ROOT / '.env').read_text(encoding='utf-8').splitlines() if (ROOT / '.env').exists() else []:
    if '=' in line and not line.lstrip().startswith('#'):
        k, v = line.split('=', 1)
        os.environ.setdefault(k.strip(), v.strip().strip('"'))
BASE = os.environ.get('HWM_API_URL', 'http://localhost:8080').rstrip('/')
SENSOR = os.environ.get('HWM_SENSOR_ADMIN_URL', 'http://127.0.0.1:5121').rstrip('/')


def call(url, method='GET', token=None, data=None):
    headers = {'Accept': 'application/json'}
    if token:
        headers['Authorization'] = f'Bearer {token}'
    if data is not None:
        headers['Content-Type'] = 'application/json'
    raw = json.dumps(data).encode() if data is not None else None
    with urlopen(Request(url, data=raw, headers=headers, method=method), timeout=12) as response:
        return json.load(response)


def wait_for(fn, expectation, timeout=14):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = fn()
        if value == expectation:
            return
        time.sleep(0.7)
    raise AssertionError(f'expected {expectation}, last observed {value}')


def main():
    user = os.environ.get('OPERATOR_USERNAME', 'operator')
    passwd = os.environ['OPERATOR_PASSWORD']
    token = call(BASE + '/api/auth/login', 'POST', data={'username': user, 'password': passwd})['token']
    api = lambda path, method='GET', data=None: call(BASE + path, method, token, data)
    original = api('/api/config/control')['config']
    old_pin = call(SENSOR + '/state')['temp_pinned']
    old_temp = call(SENSOR + '/state')['temp_c']
    channel = original['fan_channel']
    def pin(t):
        call(f'{SENSOR}/temp?value={t}', 'POST')
    def relay_on():
        channels = api('/api/relays')
        return next(item['on'] for item in channels if item['id'] == channel)
    try:
        # Reset to a known physical OFF state without competing with automation.
        api('/api/config/control', 'PUT', {**original, 'enabled': False})
        api(f'/api/relays/{channel}', 'POST', {'on': False})
        pin(25)
        wait_for(relay_on, False)
        api('/api/config/control', 'PUT', {
            **original, 'enabled': True, 'on_above': 30, 'off_below': 27})
        time.sleep(4)  # allow startup sync events to settle
        start_id = max((e['id'] for e in api('/api/events?limit=1000')), default=0)
        for t, state in [(28, False), (31, True), (29, True), (26, False)]:
            pin(t)
            wait_for(relay_on, state)
            # A hold value can be reached before the next poll; ensure a full poll passes.
            time.sleep(3)
            assert relay_on() == state, f'temperature {t} produced unexpected state'
            print(f'PASS: temp {t}C -> fan {"ON" if state else "OFF"}')
        transitions = [e for e in api('/api/events?limit=1000')
                       if e['id'] > start_id and e['kind'] == 'fan_transition']
        ons = sum('fan=on ' in e['message'] for e in transitions)
        offs = sum('fan=off ' in e['message'] for e in transitions)
        assert (ons, offs) == (1, 1), f'expected 1 ON + 1 OFF transition, got {ons}, {offs}: {transitions}'
        print('PASS: exactly one audited ON and one audited OFF transition')
    finally:
        # Keep restored configuration from racing with simulator temperature pin.
        api('/api/config/control', 'PUT', {**original, 'enabled': False})
        if old_pin:
            pin(old_temp)
        else:
            call(SENSOR + '/temp?value=auto', 'POST')
        api('/api/config/control', 'PUT', original)
        print('Original configuration and sensor pin restored')


if __name__ == '__main__':
    main()
