#!/usr/bin/env python3
"""Non-destructive, standard-library Core HTTP smoke test.
Requires `docker compose up -d --build` and .env test credentials.
"""
import json
import os
from pathlib import Path
import sys
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parents[1]
for line in (ROOT / '.env').read_text(encoding='utf-8').splitlines() if (ROOT / '.env').exists() else []:
    if '=' in line and not line.lstrip().startswith('#'):
        key, value = line.split('=', 1)
        os.environ.setdefault(key.strip(), value.strip().strip('"'))
BASE = os.getenv('HWM_API_URL', 'http://localhost:8080').rstrip('/')


def request(path, method='GET', token=None, data=None):
    headers = {'Accept': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    payload = None
    if data is not None:
        payload = json.dumps(data).encode()
        headers['Content-Type'] = 'application/json'
    req = Request(BASE + path, data=payload, method=method, headers=headers)
    try:
        with urlopen(req, timeout=8) as res:
            body = res.read()
            return res.status, json.loads(body) if body else None
    except HTTPError as e:
        try:
            body = json.loads(e.read())
        except Exception:
            body = {}
        return e.code, body


def login(prefix, fallback):
    username = os.getenv(prefix + '_USERNAME', fallback)
    password = os.getenv(prefix + '_PASSWORD', '')
    if not password:
        raise RuntimeError(prefix + '_PASSWORD missing: copy .env.example to .env')
    code, data = request('/api/auth/login', 'POST', data={'username': username, 'password': password})
    assert code == 200 and data.get('token'), (prefix, code, data)
    return data['token']


def main():
    code, result = request('/healthz')
    assert code == 200, (code, result)
    print('[OK] backend health')
    viewer = login('VIEWER', 'viewer')
    operator = login('OPERATOR', 'operator')
    for name, token in [('viewer', viewer), ('operator', operator)]:
        status, me = request('/api/auth/me', token=token)
        assert status == 200 and me['role'] == name, (status, me)
        print(f'[OK] {name} JWT login')
    code, result = request('/api/relays/0', 'POST', viewer, {'on': True})
    assert code == 403, (code, result)
    print('[OK] viewer cannot write a relay')
    code, devices = request('/api/devices', token=operator)
    assert code == 200 and isinstance(devices, list), (code, devices)
    print('[OK] device status:', ', '.join(f"{x['name']}:{x['state']}" for x in devices))
    code, samples = request('/api/measurements?source=sensor&metric=temperature_c', token=operator)
    assert code == 200 and isinstance(samples, list), (code, samples)
    print('[OK] sensor measurement endpoint:', len(samples), 'points')
    code, channels = request('/api/relays', token=operator)
    assert code == 200 and len(channels) >= 4, (code, channels)
    print('[OK] relay read-back:', len(channels), 'channels')
    # Use an auxiliary channel to avoid interacting with automatic fan channel 0.
    target = channels[3]
    code, actual = request('/api/relays/3', 'POST', operator, {'on': target['on']})
    assert code == 200 and actual['id'] == 3 and actual['on'] == target['on'], (code, actual)
    print('[OK] operator coil write + confirmed read-back')
    code, cfg = request('/api/config/control', token=operator)
    assert code == 200 and cfg.get('config'), (code, cfg)
    print('[OK] live hysteresis config')
    code, events = request('/api/events?limit=10', token=operator)
    assert code == 200 and isinstance(events, list), (code, events)
    print('[OK] event log')
    print('Core HTTP smoke test PASS. Fault injection, Grafana and race testing require separate manual checks.')


if __name__ == '__main__':
    try:
        main()
    except (AssertionError, URLError, RuntimeError) as ex:
        print('FAILED:', ex, file=sys.stderr)
        raise SystemExit(1)
