import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Activity, AlertTriangle, CheckCircle2, Cpu, ExternalLink, Lock, LogOut, Power, RefreshCw, Shield, Sliders } from 'lucide-react';
import { api, ApiError } from './api';
import { createPolling, createSnapshotGuard } from './polling';
import { LiveChart } from './LiveChart';
import { ExtendedPanels } from './ExtendedPanels';
import { ThermocoupleSnapshot, SpeedState, AuditEvent, ControlSnapshot, DeviceStatus, MeasurementPoint, RelayChannel, UserMe } from './types';

function resetTargetForDevice(name: string) {
  if (name === 'sensor') return 'temp-humidity';
  if (name === 'fan_speed') return 'speed-pwm';
  return name;
}

export function App() {
  const [token, setToken] = useState<string | null>(() => localStorage.getItem('hwm_token'));
  const [me, setMe] = useState<UserMe | null>(null);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [authError, setAuthError] = useState<string | null>(null);
  const [loadingLogin, setLoadingLogin] = useState(false);

  const [thermocouple, setThermocouple] = useState<ThermocoupleSnapshot|null>(null);
  const [speed, setSpeed] = useState<SpeedState|null>(null);
  const speedRevision = useRef(0);
  const relaySnapshots = useRef(createSnapshotGuard());
  const controlRevision = useRef(0);
  const resetEpoch = useRef<Record<string, number>>({});
  const resetPending = useRef(new Set<string>());
  const [resetting, setResetting] = useState<Record<string,boolean>>({});
  const [thermoAt, setThermoAt] = useState(0);
 const [speedAt,setSpeedAt]=useState(0);
  const [backendOffline, setBackendOffline] = useState(false);
  const [devices, setDevices] = useState<DeviceStatus[]>([]);
  const [temps, setTemps] = useState<MeasurementPoint[]>([]);
  const [humids, setHumids] = useState<MeasurementPoint[]>([]);
  const [sensorFetchFailed, setSensorFetchFailed] = useState(false);
  const [clock, setClock] = useState(() => Date.now());
  const [relays, setRelays] = useState<RelayChannel[]>([]);
  const [relayError, setRelayError] = useState<string | null>(null);
  const [control, setControl] = useState<ControlSnapshot | null>(null);
  const [events, setEvents] = useState<AuditEvent[]>([]);
  // This value comes from Prometheus's query API, not the backend's sensor API.
  const [prometheusTemperature, setPrometheusTemperature] = useState<number | null>(null);
  const [prometheusError, setPrometheusError] = useState<string | null>(null);


  const [busyRelay, setBusyRelay] = useState<number | null>(null);
  const [actionMsg, setActionMsg] = useState<{ type: 'ok' | 'err'; text: string } | null>(null);

  const [onAboveInput, setOnAboveInput] = useState('30');
  const [offBelowInput, setOffBelowInput] = useState('27');
  const [fanChInput, setFanChInput] = useState('0');
  const [savingCfg, setSavingCfg] = useState(false);

  const logout = useCallback((reason?: string) => {
    localStorage.removeItem('hwm_token');
    setToken(null);
    setMe(null);
    if (reason) setAuthError(reason);
  }, []);

  const hasPerm = useCallback(
    (cap: string) => Boolean(me?.perms?.includes(cap)),
    [me]
  );

  // Health is independent of device failures and caller capabilities.
  useEffect(() => {
    const polling = createPolling(async signal => {
      try {
        const health = await api.health(signal);
        if (!signal.aborted) setBackendOffline(health?.status !== 'ok');
      } catch {
        if (!signal.aborted) setBackendOffline(true);
      }
    }, 2000);
    return () => polling.stop();
  }, []);

  // Retry a transient profile failure; abandon responses from an old session.
  useEffect(() => {
    if (!token) return;
    const polling = createPolling(async signal => {
      try {
        const user = await api.me(token, signal);
        if (signal.aborted) return;
        setMe(user);
        setAuthError(null);
        polling.stop();
      } catch (err) {
        if (!signal.aborted && err instanceof ApiError && err.status === 401) {
          logout(err.code === 'token_expired' ? 'Session expired. Please sign in again.' : 'Unauthorized session.');
        }
      }
    }, 2000);
    return () => polling.stop();
  }, [token, logout]);

  useEffect(() => {
    const id = window.setInterval(() => setClock(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, []);

  const telemetryPolling = useRef<ReturnType<typeof createPolling> | null>(null);
  const pollAll = useCallback(() => telemetryPolling.current?.run(), []);

  // Cleanup aborts the old session; manual refresh shares the scheduled cycle.
  useEffect(() => {
    if (!token || !me) return;
    const polling = createPolling(async signal => {
      try {
        const tasks: Promise<void>[] = [];
        const speedVersion = speedRevision.current;
        const controlVersion = controlRevision.current;
        const relayVersion = relaySnapshots.current.capture();
        const resetVersion = { ...resetEpoch.current };
        const pendingResets = new Set(resetPending.current);
        const acceptsResetSnapshot = (target: string) =>
          !signal.aborted && !pendingResets.has(target) && resetVersion[target] === resetEpoch.current[target];

        if (hasPerm('read:thermocouple')) {
          tasks.push(api.thermocouple(token, signal).then(value => {
            if (!acceptsResetSnapshot('thermocouple')) return;
            setThermocouple(value);
            setThermoAt(Date.now());
          }).catch(error => {
            if (acceptsResetSnapshot('thermocouple')) setThermocouple(null);
            if (error instanceof ApiError && error.status === 401) throw error;
          }));
        }
        if (hasPerm('read:fan_speed')) {
          const acceptsSpeed = () =>
            acceptsResetSnapshot('speed-pwm') && speedVersion === speedRevision.current;
          tasks.push(api.speed(token, signal).then(value => {
            if (!acceptsSpeed()) return;
            setSpeed(value);
            setSpeedAt(Date.now());
          }).catch(error => {
            if (acceptsSpeed()) setSpeed(null);
            if (error instanceof ApiError && error.status === 401) throw error;
          }));
        }
        if (hasPerm('read:device_status')) {
          tasks.push(api.devices(token, signal).then(value => {
            if (signal.aborted) return;
            setDevices(previous => value.map(device => {
              if (acceptsResetSnapshot(resetTargetForDevice(device.name))) return device;
              return previous.find(item => item.name === device.name) ?? {
                name: device.name, state: 'recovering', consecutive_failures: 0,
              };
            }));
            setResetting(previous => {
              const next = { ...previous };
              for (const device of value) {
                const target = resetTargetForDevice(device.name);
                if (device.state === 'online' && acceptsResetSnapshot(target)) delete next[target];
              }
              return next;
            });
          }));
        }
        if (hasPerm('read:sensor')) {
          tasks.push(Promise.allSettled([
            api.measurements(token, 'sensor', 'temperature_c', 50, signal),
            api.measurements(token, 'sensor', 'humidity_pct', 50, signal),
          ]).then(([temperature, humidity]) => {
            if (signal.aborted) return;
            const failures = [temperature, humidity].filter(result => result.status === 'rejected');
            const failure = failures.find(result => result.reason instanceof ApiError && result.reason.status === 401) ?? failures[0];
            if (failure) throw failure.reason;
            if (temperature.status !== 'fulfilled' || humidity.status !== 'fulfilled') return;
            setTemps(temperature.value);
            setHumids(humidity.value);
            setSensorFetchFailed(false);
          }).catch(err => {
            if (signal.aborted) return;
            setSensorFetchFailed(true);
            if (err instanceof ApiError && err.status === 401) throw err;
          }));
        }
        if (hasPerm('read:relay')) {
          tasks.push(
            api.relays(token, signal).then((channels) => {
              if (signal.aborted || !relaySnapshots.current.accepts(relayVersion)) return;
              setRelays(channels); setRelayError(null);
            }).catch((error) => {
              if (error instanceof ApiError && error.status === 401) throw error;
              if (signal.aborted || !relaySnapshots.current.accepts(relayVersion)) return;
              setRelays([]); // never portray a stale coil state as an actual read-back
              setRelayError(error instanceof ApiError ? error.code : 'relay_unreachable');
            })
          );
        }
        if (hasPerm('read:events')) {
          tasks.push(api.events(token, 30, signal).then(value => { if (!signal.aborted) setEvents(value); }));
          tasks.push(
            api.getControl(token, signal).then((snap) => {
              if (signal.aborted) return;
              if (controlVersion !== controlRevision.current) return;
              if (!acceptsResetSnapshot('relay') || !acceptsResetSnapshot('temp-humidity')) return;
              setControl(snap);
              setOnAboveInput((prev) => (document.activeElement?.id === 'on_above' ? prev : String(snap.config.on_above)));
              setOffBelowInput((prev) => (document.activeElement?.id === 'off_below' ? prev : String(snap.config.off_below)));
              setFanChInput((prev) => (document.activeElement?.id === 'fan_ch' ? prev : String(snap.config.fan_channel)));
            })
          );
        }

        // Wait for every endpoint, even when one fails early.
        const results = await Promise.allSettled(tasks);
        if (signal.aborted) return;
        const unauthorized = results.find(result => result.status === 'rejected' &&
          result.reason instanceof ApiError && result.reason.status === 401);
        if (unauthorized?.status === 'rejected') throw unauthorized.reason;
      } catch (err) {
        if (signal.aborted) return;
        if (err instanceof ApiError && err.status === 401) {
          logout('Session expired. Please log in again.');
          return;
        }
      }
    }, 2000);
    telemetryPolling.current = polling;
    return () => {
      telemetryPolling.current = null;
      polling.stop();
    };
  }, [token, me, hasPerm, logout]);

  useEffect(() => {
    if (!token || !me || !hasPerm('read:sensor')) {
      setPrometheusTemperature(null);
      setPrometheusError(null);
      return;
    }

    const polling = createPolling(async signal => {
      try {
        const response = await fetch('/prometheus/api/v1/query?query=hwm_sensor_temperature_celsius', {
          cache: 'no-store',
          signal: AbortSignal.any([signal, AbortSignal.timeout(10000)]),
        });
        if (!response.ok) throw new Error(`Prometheus HTTP ${response.status}`);
        const payload: {
          status?: string;
          data?: { result?: Array<{ value?: [number, string] }> };
        } = await response.json();
        const sample = payload.data?.result?.[0]?.value?.[1];
        const value = sample === undefined ? NaN : Number(sample);
        if (payload.status !== 'success' || !Number.isFinite(value)) {
          throw new Error('No current Prometheus temperature sample');
        }
        if (!signal.aborted) {
          setPrometheusTemperature(value);
          setPrometheusError(null);
        }
      } catch (error) {
        if (!signal.aborted) {
          setPrometheusTemperature(null);
          setPrometheusError(error instanceof Error ? error.message : 'Prometheus unavailable');
        }
      }
    }, 5000);
    return () => polling.stop();
  }, [token, me, hasPerm]);

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoadingLogin(true);
    setAuthError(null);
    try {
      const res = await api.login(username, password);
      localStorage.setItem('hwm_token', res.token);
      setToken(res.token);
    } catch (err) {
      setAuthError(err instanceof ApiError ? `Login failed: ${err.code}` : 'Backend unreachable');
    } finally {
      setLoadingLogin(false);
    }
  };

  const toggleRelay = async (ch: RelayChannel) => {
    if (!token || !hasPerm('write:relay') || !relayDeviceOnline || backendOffline || relaySnapshots.current.busy) return;
    if (control?.config.enabled && ch.id === control.config.fan_channel) {
      setActionMsg({ type: 'err', text: 'Fan is in AUTO mode. Select MANUAL and save the control mode before toggling Fan 1.' });
      return;
    }
    const operation = relaySnapshots.current.begin();
    setBusyRelay(ch.id);
    setActionMsg(null);
    try {
      const updated = await api.setRelay(token, ch.id, !ch.on);
      if (!operation.current()) return;
      setRelays((prev) => prev.map((r) => (r.id === updated.id ? updated : r)));
      setActionMsg({ type: 'ok', text: `Confirmed ${updated.label} -> ${updated.on ? 'ON' : 'OFF'} (read-back verified)` });
    } catch (err) {
      if (!operation.current()) return;
      const msg = err instanceof ApiError ? err.code : 'relay_write_failed';
      setActionMsg({ type: 'err', text: `Relay ${ch.label} failed: ${msg}` });
      setRelays([]);
      setRelayError(msg);
    } finally {
      operation.finish();
      setBusyRelay(null);
      pollAll();
    }
  };

  const saveControlConfig = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!token || !hasPerm('write:thresholds')) return;
    setSavingCfg(true);
    setActionMsg(null);
    try {
      const snap = await api.putControl(token, {
        on_above: parseFloat(onAboveInput),
        off_below: parseFloat(offBelowInput),
        fan_channel: parseInt(fanChInput, 10),
      });
      setControl(snap);
      setActionMsg({ type: 'ok', text: `Control configuration saved: ${snap.config.enabled ? 'AUTO' : 'MANUAL'} mode. Changes audited.` });
      pollAll();
    } catch (err) {
      setActionMsg({ type: 'err', text: `Config update rejected: ${err instanceof ApiError ? err.code : 'error'}` });
    } finally {
      setSavingCfg(false);
    }
  };

  if (!token || !me) {
    return (
      <div className="min-h-screen flex items-center justify-center p-4 bg-slate-950">
        <div className="w-full max-w-md bg-slate-900 border border-slate-800 rounded-2xl p-6 shadow-xl">
          <div className="flex items-center gap-3 mb-6">
            <div className="p-2.5 bg-indigo-500/10 border border-indigo-500/30 rounded-xl text-indigo-400">
              <Cpu className="w-6 h-6" />
            </div>
            <div>
              <h1 className="text-lg font-semibold text-white">Industrial Hardware Platform</h1>
              <p className="text-xs text-slate-400">Capability-Gated Monitoring & Control</p>
            </div>
          </div>

          {authError && (
            <div className="mb-4 p-3 rounded-lg bg-red-500/10 border border-red-500/30 text-red-300 text-sm flex items-center gap-2">
              <AlertTriangle className="w-4 h-4 shrink-0" />
              <span>{authError}</span>
            </div>
          )}

          <form onSubmit={handleLogin} className="space-y-4">
            <div>
              <label className="block text-xs font-medium text-slate-400 mb-1">Username</label>
              <input
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                className="w-full px-3 py-2 bg-slate-950 border border-slate-800 rounded-lg text-sm text-white focus:outline-none focus:border-indigo-500"
                required
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-slate-400 mb-1">Password</label>
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className="w-full px-3 py-2 bg-slate-950 border border-slate-800 rounded-lg text-sm text-white focus:outline-none focus:border-indigo-500"
                required
              />
            </div>
            <button
              type="submit"
              disabled={loadingLogin}
              className="w-full py-2.5 bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 text-white font-medium rounded-lg text-sm transition"
            >
              {loadingLogin ? 'Signing in...' : 'Sign In'}
            </button>
          </form>


        </div>
      </div>
    );
  }

  const canWriteRelay = hasPerm('write:relay');
  const relayDeviceOnline = !resetting.relay && devices.some(d => d.name === 'relay' && d.state === 'online');
  const pwmResetVersion = resetEpoch.current['speed-pwm'];
  const sensorDeviceOnline = devices.some(d => d.name === 'sensor' && d.state === 'online');
  const canWriteThresholds = hasPerm('write:thresholds');
  const sensorLatestAt = Math.max(0, ...[temps[temps.length - 1], humids[humids.length - 1]]
    .filter((point): point is MeasurementPoint => Boolean(point))
    .map(point => new Date(point.at).getTime()));
  const sensorQuality: 'live' | 'stale' | 'unknown' =
    backendOffline || resetting['temp-humidity'] || sensorFetchFailed || !sensorDeviceOnline || sensorLatestAt === 0
      ? 'unknown'
      : clock - sensorLatestAt > 10000 ? 'stale' : 'live';
  const autoFanChannel = control?.config.fan_channel;
  const autoFanActive = control?.config.enabled === true;

  return (
    <div className="min-h-screen bg-slate-950 text-slate-100">
      {/* Top Bar */}
      <header className="border-b border-slate-800 bg-slate-900/80 backdrop-blur sticky top-0 z-10">
        <div className="max-w-7xl mx-auto px-4 py-3 flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-3">
            <Cpu className="w-6 h-6 text-indigo-400" />
            <div>
              <span className="font-semibold text-white">HWM Control Plane</span>
              <span className="ml-2 text-xs px-2 py-0.5 rounded-full bg-slate-800 text-slate-300">
                Modbus TCP
              </span>
            </div>
          </div>

          <div className="flex items-center gap-3">
            <div className="flex items-center gap-2 text-xs bg-slate-950 border border-slate-800 px-3 py-1.5 rounded-lg">
              <Shield className="w-3.5 h-3.5 text-emerald-400" />
              <span className="font-medium text-slate-200">{me.username}</span>
              <span className="text-slate-500">({me.role})</span>
            </div>
            <button
              onClick={() => logout()}
              className="flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-lg bg-slate-800 hover:bg-slate-700 text-slate-200"
            >
              <LogOut className="w-3.5 h-3.5" /> Logout
            </button>
          </div>
        </div>
      </header>

      <main className="max-w-7xl mx-auto px-4 py-6 space-y-6">
        {/* Backend Connectivity Banner */}
        {backendOffline && (
          <div className="p-4 rounded-xl bg-red-500/10 border border-red-500/40 text-red-200 flex items-center gap-3">
            <AlertTriangle className="w-5 h-5 text-red-400 shrink-0" />
            <div className="text-sm">
              <strong>Backend Unreachable:</strong> Live telemetry polling is degraded. Retrying every 2s...
            </div>
          </div>
        )}

        {relayError && hasPerm('read:relay') && (
          <div role="alert" className="p-3 rounded-xl bg-amber-500/10 border border-amber-500/40 text-amber-200 text-sm">
            Relay status unknown: {relayError}. Controls are disabled until a verified read-back succeeds.
          </div>
        )}
        {hasPerm('read:sensor') && !sensorDeviceOnline && (
          <div role="status" className="p-3 rounded-xl bg-amber-500/10 border border-amber-500/40 text-amber-200 text-sm">
            Sensor offline or waiting for first read. Historical chart points are not current measurements.
          </div>
        )}

        {/* Action Feedback Toast */}
        {actionMsg && (
          <div
            className={`p-3 rounded-xl border text-sm flex items-center justify-between ${
              actionMsg.type === 'ok'
                ? 'bg-emerald-500/10 border-emerald-500/30 text-emerald-200'
                : 'bg-red-500/10 border-red-500/30 text-red-200'
            }`}
          >
            <span>{actionMsg.text}</span>
            <button onClick={() => setActionMsg(null)} className="text-xs underline ml-4">
              Dismiss
            </button>
          </div>
        )}

        {/* Device Connectivity Cards */}
        <section className="grid grid-cols-1 md:grid-cols-3 gap-4">
          {devices.map((d) => {
            const isOnline = d.state === 'online';
            const isDegraded = d.state === 'degraded';
            return (
              <div key={d.name} className="bg-slate-900 border border-slate-800 rounded-xl p-4 flex items-center justify-between">
                <div>
                  <div className="text-xs uppercase tracking-wider text-slate-400">Field Device</div>
                  <div className="text-base font-semibold text-white mt-0.5">{d.name}</div>
                  {d.last_error && (
                    <div className="text-xs text-red-400 mt-1 truncate max-w-xs" title={d.last_error}>
                      {d.last_error}
                    </div>
                  )}
                </div>
                <span
                  className={`px-2.5 py-1 rounded-full text-xs font-medium flex items-center gap-1.5 ${
                    isOnline
                      ? 'bg-emerald-500/15 text-emerald-300 border border-emerald-500/30'
                      : isDegraded
                      ? 'bg-amber-500/15 text-amber-300 border border-amber-500/30'
                      : 'bg-red-500/15 text-red-300 border border-red-500/30'
                  }`}
                >
                  {isOnline ? <CheckCircle2 className="w-3.5 h-3.5" /> : <AlertTriangle className="w-3.5 h-3.5" />}
                  {d.state.toUpperCase()}
                </span>
              </div>
            );
          })}

          {/* Auto Control Status Summary */}
          {control && (
            <div className="bg-slate-900 border border-slate-800 rounded-xl p-4 flex items-center justify-between">
              <div>
                <div className="text-xs uppercase tracking-wider text-slate-400">Hysteresis Fan Loop</div>
                <div className="text-base font-semibold text-white mt-0.5">
                  {control.suspended
                    ? 'SUSPENDED (Stale/Offline)'
                    : control.config.enabled
                    ? `AUTO (${!relayDeviceOnline || resetting['temp-humidity'] || control.fan_on === null ? 'UNKNOWN' : control.fan_on ? 'FAN ON' : 'FAN OFF'})`
                    : 'MANUAL (AUTO OFF)'}
                </div>
                <div className="text-xs text-slate-400 mt-0.5">
                  Band: {control.config.off_below}°C – {control.config.on_above}°C (Ch {control.config.fan_channel})
                </div>
              </div>
              <RefreshCw className={`w-5 h-5 ${control.suspended ? 'text-amber-400' : 'text-indigo-400'}`} />
            </div>
          )}

          {/* Caller Granted Capabilities */}
          <div className="bg-slate-900 border border-slate-800 rounded-xl p-4">
            <div className="text-xs uppercase tracking-wider text-slate-400 mb-1.5">Granted Capabilities</div>
            <div className="flex flex-wrap gap-1">
              {me.perms.map((p) => (
                <span key={p} className="text-[11px] px-2 py-0.5 rounded bg-slate-800 text-slate-300 font-mono">
                  {p}
                </span>
              ))}
            </div>
          </div>
        </section>

        {/* Live metric queried from Prometheus (not the ordinary telemetry API). */}
        {hasPerm('read:sensor') && (
          <section className="bg-slate-900 border border-slate-800 rounded-xl p-5">
            <div className="flex flex-wrap items-center justify-between gap-3 mb-3">
              <div>
                <h2 className="font-semibold text-white">Prometheus / Grafana Observability</h2>
                <p className="text-xs text-slate-400">Source: Prometheus query API · hwm_sensor_temperature_celsius · refresh every 5s</p>
              </div>
              <a
                href={`${window.location.protocol}//${window.location.hostname}:${import.meta.env.VITE_GRAFANA_PORT || '3001'}/d/hwm-industrial-overview`}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1.5 rounded-lg bg-indigo-600 px-3 py-2 text-xs font-medium text-white hover:bg-indigo-500"
              >
                Open Grafana Dashboard <ExternalLink className="w-3.5 h-3.5" />
              </a>
            </div>
            {prometheusTemperature !== null ? (
              <div className="text-3xl font-semibold text-emerald-300">
                {prometheusTemperature.toFixed(1)} °C
                <span className="ml-3 text-xs font-normal text-slate-400">Live Prometheus sample</span>
              </div>
            ) : (
              <div className="text-sm text-amber-300">
                Prometheus data unavailable: {prometheusError || 'Waiting for first scrape'}
              </div>
            )}
          </section>
        )}

        {/* Device state is authoritative; database history remains viewable when stale. */}
        {hasPerm('read:sensor') && (
          <div role="status" className={`rounded-lg px-4 py-2 text-sm border ${sensorQuality === 'live' ? 'bg-emerald-500/10 border-emerald-500/30 text-emerald-300' : 'bg-amber-500/10 border-amber-500/30 text-amber-300'}`}>
            Sensor telemetry: <strong>{sensorQuality.toUpperCase()}</strong>
            {sensorLatestAt > 0 ? ` · last sample ${new Date(sensorLatestAt).toLocaleTimeString()} (${Math.max(0, Math.round((clock - sensorLatestAt) / 1000))}s ago)` : ' · no sample received'}
            {sensorQuality !== 'live' && ' · charts below are historical and must not be used for live control'}
          </div>
        )}
        {/* Live Telemetry Charts (historical samples; connectivity is shown independently) */}
        {hasPerm('read:sensor') && (
          <section className="grid grid-cols-1 lg:grid-cols-2 gap-4">
            <LiveChart
              title="Ambient Temperature (Modbus Input Reg 0)"
              unit="°C"
              color="#f97316"
              points={temps}
              quality={sensorQuality}
              upperThreshold={control?.config.on_above}
              lowerThreshold={control?.config.off_below}
            />
            <LiveChart
              title="Relative Humidity (Modbus Input Reg 1)"
              unit="%"
              color="#38bdf8"
              points={humids}
              quality={sensorQuality}
            />
          </section>
        )}

        {/* Controls & Threshold Config */}
        <section className="grid grid-cols-1 gap-6">
          {/* Relay Card */}
          {hasPerm('read:relay') && (
            <div className="bg-slate-900 border border-slate-800 rounded-xl p-5">
              <div className="flex items-center justify-between mb-4">
                <div className="flex items-center gap-2">
                  <Power className="w-5 h-5 text-indigo-400" />
                  <h2 className="font-semibold text-white">Relay Output Card (Coils 0–7)</h2>
                </div>
                {!canWriteRelay && (
                  <span className="flex items-center gap-1 text-xs text-amber-300 bg-amber-500/10 border border-amber-500/30 px-2.5 py-1 rounded-full">
                    <Lock className="w-3 h-3" /> Missing write:relay
                  </span>
                )}
              </div>

              <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
                {relays.map((ch) => (
                  <button
                    key={ch.id}
                    type="button"
                    disabled={!canWriteRelay || !relayDeviceOnline || backendOffline || busyRelay !== null || (autoFanActive && ch.id === autoFanChannel)}
                    onClick={() => toggleRelay(ch)}
                    title={!canWriteRelay ? 'Requires write:relay capability' : (autoFanActive && ch.id === autoFanChannel) ? 'Fan is controlled automatically. Change mode to MANUAL and save first.' : `Toggle ${ch.label}`}
                    className={`p-3.5 rounded-xl border text-left transition flex flex-col justify-between gap-3 ${
                      ch.on
                        ? 'bg-emerald-500/15 border-emerald-500/40 text-emerald-100'
                        : 'bg-slate-950 border-slate-800 text-slate-300'
                    } ${
                      (!canWriteRelay || !relayDeviceOnline || backendOffline || (autoFanActive && ch.id === autoFanChannel))
                        ? 'opacity-60 cursor-not-allowed'
                        : 'hover:border-indigo-500 cursor-pointer'
                    }`}
                  >
                    <div className="flex items-center justify-between w-full">
                      <span className="text-xs font-mono text-slate-400">CH {ch.id}</span>
                      <span
                        className={`w-2.5 h-2.5 rounded-full ${
                          ch.on ? 'bg-emerald-400 shadow-[0_0_8px_#34d399]' : 'bg-slate-600'
                        }`}
                      />
                    </div>
                    <div>
                      <div className="font-medium text-sm truncate">{ch.label}</div>
                      <div className="text-xs font-semibold mt-0.5">
                        {busyRelay === ch.id ? 'WRITING...' : ch.on ? 'ON' : 'OFF'}
                        {autoFanActive && ch.id === autoFanChannel && <span className="block text-[10px] text-amber-300">AUTO LOCK</span>}
                      </div>
                    </div>
                  </button>
                ))}
              </div>
            </div>
          )}


        </section>

        <ExtendedPanels
          key={token}
          token={token}
          hasPerm={hasPerm}
          thermocouple={thermocouple}
          speed={speed}
          control={control}
          devices={devices}
          resetting={resetting}
          thermoLive={!backendOffline && !resetting.thermocouple && clock - thermoAt <= 6000}
          speedLive={!backendOffline && !resetting['speed-pwm'] && clock - speedAt <= 6000}
          backendOffline={backendOffline}
          fanConfig={hasPerm('read:events') ? (
            <div className="border-t border-slate-800 pt-5 mt-5 space-y-3">
              <div className="flex items-center justify-between gap-3">
                <div className="flex items-center gap-2"><Sliders className="w-4 h-4 text-indigo-400" /><h3 className="font-medium text-white">Auto Fan Control &amp; Hysteresis</h3></div>
                {!canWriteThresholds && <span className="text-xs text-amber-300">Read-only</span>}
              </div>
<p className="text-xs text-slate-400 mb-3">
  AUTO: temperature controls the fan; the configured fan relay is locked.
  MANUAL: authorized operators may toggle the fan relay directly.
  Use AUTO/MANUAL buttons above to change mode; Save Thresholds applies the hysteresis settings.
</p>
<form onSubmit={saveControlConfig} className="space-y-3">
  <div className="grid grid-cols-2 gap-3">
    <div>
      <label className="block text-xs text-slate-400 mb-1">On Above (°C)</label>
      <input
        id="on_above"
        type="number"
        step="0.5"
        disabled={!canWriteThresholds}
        value={onAboveInput}
        onChange={(e) => setOnAboveInput(e.target.value)}
        className="w-full px-3 py-1.5 bg-slate-950 border border-slate-800 rounded-lg text-sm disabled:opacity-60"
      />
    </div>
    <div>
      <label className="block text-xs text-slate-400 mb-1">Off Below (°C)</label>
      <input
        id="off_below"
        type="number"
        step="0.5"
        disabled={!canWriteThresholds}
        value={offBelowInput}
        onChange={(e) => setOffBelowInput(e.target.value)}
        className="w-full px-3 py-1.5 bg-slate-950 border border-slate-800 rounded-lg text-sm disabled:opacity-60"
      />
    </div>
  </div>

  <div>
    <label className="block text-xs text-slate-400 mb-1">Target Fan Relay Channel (0–7)</label>
    <input
      id="fan_ch"
      type="number"
      min="0"
      max="7"
      disabled={!canWriteThresholds}
      value={fanChInput}
      onChange={(e) => setFanChInput(e.target.value)}
      className="w-full px-3 py-1.5 bg-slate-950 border border-slate-800 rounded-lg text-sm disabled:opacity-60"
    />
  </div>

  <button
    type="submit"
    disabled={!canWriteThresholds || savingCfg}
    className="w-full py-2 rounded-lg bg-indigo-600 hover:bg-indigo-500 disabled:bg-slate-800 disabled:text-slate-500 disabled:cursor-not-allowed text-sm font-medium transition"
  >
    {!canWriteThresholds
      ? 'Requires write:thresholds'
      : savingCfg
      ? 'Saving...'
      : 'Save Thresholds'}
  </button>
</form>
            </div>
          ) : null}
          onControl={snap => {
            controlRevision.current++;
            setControl(snap);
            pollAll();
          }}
          onSpeed={value => {
            if (pwmResetVersion !== resetEpoch.current['speed-pwm'] || resetPending.current.has('speed-pwm')) return;
            speedRevision.current++;
            setSpeed(value);
            setSpeedAt(Date.now());
            pollAll();
          }}
          onReset={async target => {
            resetEpoch.current[target] = (resetEpoch.current[target] ?? 0) + 1;
            resetPending.current.add(target);
            const relayReset = target === 'relay' ? relaySnapshots.current.begin() : undefined;
            setResetting(previous => ({ ...previous, [target]: true }));
            setDevices(previous => previous.map(device => resetTargetForDevice(device.name) === target
              ? { name: device.name, state: 'recovering', consecutive_failures: 0 }
              : device));
            if (target === 'relay' || target === 'temp-humidity') {
              setControl(previous => previous && { ...previous, fan_on: null });
            }
            if (target === 'relay') setRelays([]);
            if (target === 'thermocouple') setThermocouple(null);
            if (target === 'speed-pwm') { speedRevision.current++; setSpeed(null); }
            try {
              await api.reset(token, target);
            } finally {
              resetPending.current.delete(target);
              relayReset?.finish();
              pollAll();
            }
          }}
        />

        {/* Audit & Event Log */}
        {hasPerm('read:events') && (
          <section className="bg-slate-900 border border-slate-800 rounded-xl p-5">
            <div className="flex items-center gap-2 mb-3">
              <Activity className="w-5 h-5 text-indigo-400" />
              <h2 className="font-semibold text-white">Audit & Event Log</h2>
            </div>
            <div className="overflow-x-auto max-h-64 overflow-y-auto">
              <table className="w-full text-left text-xs">
                <thead className="text-slate-400 border-b border-slate-800 sticky top-0 bg-slate-900">
                  <tr>
                    <th className="py-2 pr-3">Time</th>
                    <th className="py-2 pr-3">Kind</th>
                    <th className="py-2 pr-3">Source</th>
                    <th className="py-2 pr-3">Actor</th>
                    <th className="py-2">Details</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-800/60">
                  {events.map((ev, idx) => (
                    <tr key={ev.id ?? idx} className="hover:bg-slate-800/30">
                      <td className="py-2 pr-3 text-slate-400 whitespace-nowrap">
                        {new Date(ev.at).toLocaleTimeString()}
                      </td>
                      <td className="py-2 pr-3 font-mono text-indigo-300">{ev.kind}</td>
                      <td className="py-2 pr-3 text-slate-300">{ev.source}</td>
                      <td className="py-2 pr-3 text-slate-300">{ev.actor || 'system'}</td>
                      <td className="py-2 text-slate-200 font-mono">{ev.message}</td>
                    </tr>
                  ))}
                  {events.length === 0 && (
                    <tr>
                      <td colSpan={5} className="py-4 text-center text-slate-500">
                        No events recorded yet.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </section>
        )}
      </main>
    </div>
  );
}

export default App;
