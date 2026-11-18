#!/usr/bin/env python3
"""Opt-in live Modbus fault/recovery test for the Core sensor.

Only use against a local test stack. The simulator's existing fault mode is
restored even when assertions fail. No third-party Python packages needed.
"""
import argparse
import json
import os
import sys
import time
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parents[1]
for line in (ROOT / '.env').read_text(encoding='utf-8').splitlines() if (ROOT / '.env').exists() else []:
    if '=' in line and not line.lstrip().startswith('#'):
        key, val = line.split('=', 1)
        os.environ.setdefault(key.strip(), val.strip().strip('"'))

BASE = os.getenv('HWM_API_URL', 'http://localhost:8080').rstrip('/')
SENSOR = os.getenv('HWM_SENSOR_ADMIN_URL', 'http://127.0.0.1:5121').rstrip('/')


def request(url, *, method='GET', token=None, payload=None, timeout=8):
    headers = {'Accept': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    if payload is not None:
        headers['Content-Type'] = 'application/json'
    raw = json.dumps(payload).encode('utf-8') if payload is not None else None
    req = Request(url, data=raw, headers=headers, method=method)
    with urlopen(req, timeout=timeout) as response:
        raw = response.read().decode('utf-8')
        return json.loads(raw) if raw else None


def wait_until(label, probe, timeout):
    end = time.monotonic() + timeout
    last = None
    while time.monotonic() < end:
        try:
            last = probe()
            if last:
                print(f'[OK] {label}')
                return
        except (HTTPError, URLError, TimeoutError, ValueError) as exc:
            last = str(exc)
        time.sleep(1.0)
    raise AssertionError(f'{label} did not succeed in {timeout}s; last={last!r}')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mode', choices=['timeout', 'garbage', 'malformed', 'down', 'both', 'all'], default='all')
    parser.add_argument('--max-wait', type=int, default=100,
                        help='Seconds allowed for each offline/recovery transition')
    args = parser.parse_args()
    if args.max_wait < 10:
        parser.error('--max-wait must be at least 10 seconds')

    username = os.getenv('OPERATOR_USERNAME', 'operator')
    password = os.getenv('OPERATOR_PASSWORD')
    if not password:
        raise RuntimeError('OPERATOR_PASSWORD not configured in .env')
    token = request(BASE + '/api/auth/login', method='POST',
                    payload={'username': username, 'password': password})['token']
    def api(path):
        return request(BASE + path, token=token)
    def sensor_status():
        return next(x for x in api('/api/devices') if x['name'] == 'sensor')
    def fault(mode, delay=None):
        params = {'mode': mode}
        if delay:
            params['delay'] = delay
        return request(SENSOR + '/fault?' + urlencode(params), method='POST')
    def backend_alive():
        return request(BASE + '/healthz') is not None
    def metric_offline():
        with urlopen(BASE + '/metrics', timeout=8) as response:
            body = response.read().decode('utf-8')
        return ('hwm_device_online{device="sensor",state="offline"} 0' in body
                and 'hwm_sensor_temperature_celsius' not in body
                and 'hwm_sensor_humidity_percent' not in body)

    # Reject an already faulted simulator so tests never discard someone else's test state.
    original = request(SENSOR + '/state')
    if original['fault'] != 'none':
        raise RuntimeError(f"Sensor is already in fault mode {original['fault']!r}; "
                           "reset it to none before running this test")
    if sensor_status()['state'] != 'online':
        raise RuntimeError('Sensor is not online before test; cannot establish baseline')
    print('[OK] baseline: sensor online')

    modes = {'both': ['timeout', 'garbage'],
             'all': ['timeout', 'garbage', 'malformed', 'down']}.get(args.mode, [args.mode])
    try:
        for mode in modes:
            print(f'\n--- Injecting {mode} ---')
            # timeout mode deliberately exceeds the 2-second Go client timeout.
            fault(mode, '5s' if mode == 'timeout' else None)
            wait_until(f'{mode}: sensor offline after retries',
                       lambda: sensor_status()['state'] == 'offline', args.max_wait)
            assert backend_alive(), 'Go backend health endpoint failed during fault'
            print(f'[OK] {mode}: backend remained healthy')
            wait_until(f'{mode}: Prometheus reports sensor offline', metric_offline, 12)
            # A rejected Modbus reply must not update last_seen during the fault.
            last_seen = sensor_status()['last_seen']
            time.sleep(4)
            still = sensor_status()
            assert still['state'] == 'offline', (mode, still)
            assert still['last_seen'] == last_seen, f'{mode}: invalid data advanced last_seen'
            if mode == 'malformed':
                assert still['last_error'] == 'read input registers: protocol error', still
            print(f'[OK] {mode}: no false successful sample / last_seen unchanged')
            fault('none')
            wait_until(f'{mode}: sensor recovered online',
                       lambda: sensor_status()['state'] == 'online', args.max_wait)
            assert backend_alive(), 'Go backend health endpoint failed after recovery'
            print(f'[OK] {mode}: recovery completed')
        print('\nPASS: ' + ' + '.join(modes) + ' fault/recovery checks')
    finally:
        # Make restoration resilient if the backend is temporarily unreachable.
        try:
            original_delay = original.get('fault_delay', '')
            fault(original['fault'], original_delay)
            print('Original simulator fault mode restored')
        except Exception as exc:
            print('CRITICAL: failed to restore simulator fault mode:', exc, file=sys.stderr)
            print(f"Manually run: POST {SENSOR}/fault?mode=none", file=sys.stderr)
            raise


if __name__ == '__main__':
    try:
        main()
    except (AssertionError, HTTPError, URLError, RuntimeError, KeyError, ValueError) as exc:
        print('FAIL:', exc, file=sys.stderr)
        raise SystemExit(1)
