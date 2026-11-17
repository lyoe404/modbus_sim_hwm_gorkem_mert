#!/usr/bin/env python3
"""Standard-library Core HTTP smoke test; restores the tested relay state.
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


def set_and_verify_relay(token, channel_id, on):
    code, actual = request(f'/api/relays/{channel_id}', 'POST', token, {'on': on})
    assert code == 200 and actual['id'] == channel_id and actual['on'] is on, (code, actual)
    # Read independently: the write response alone cannot prove the next coil read agrees.
    code, channels = request('/api/relays', token=token)
    assert code == 200 and isinstance(channels, list), (code, channels)
    channel = next((ch for ch in channels if ch['id'] == channel_id), None)
    assert channel is not None and channel['on'] is on, (channel_id, on, channels)


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
    code, cfg = request('/api/config/control', token=operator)
    assert code == 200 and cfg.get('config'), (code, cfg)
    print('[OK] live hysteresis config')
    # Prefer auxiliary CH3, but never touch the configured fan channel.
    
    candidates = [ch for ch in channels if ch['id'] != cfg['config']['fan_channel']]
    assert candidates, 'No relay available outside the fan control loop'
    target = next((ch for ch in candidates if ch['id'] == 3), candidates[0])
    channel_id, original = target['id'], target['on']
    assert isinstance(original, bool), target
    try:
        set_and_verify_relay(operator, channel_id, not original)
        print(f'[OK] CH{channel_id} {"ON" if original else "OFF"} -> '
              f'{"OFF" if original else "ON"}: write response + independent read-back')
    finally:
        # Restore even when the first write or its independent read-back fails.
        set_and_verify_relay(operator, channel_id, original)
        print(f'[OK] CH{channel_id} restored {"ON" if original else "OFF"}: '
              'write response + independent read-back')
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
