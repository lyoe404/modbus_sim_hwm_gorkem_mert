import React, { useCallback, useEffect, useState } from 'react';
import { Activity, AlertTriangle, CheckCircle2, Cpu, Lock, LogOut, Power, RefreshCw, Shield, Sliders } from 'lucide-react';
import { api, ApiError } from './api';
import { LiveChart } from './LiveChart';
import { AuditEvent, ControlSnapshot, DeviceStatus, MeasurementPoint, RelayChannel, UserMe } from './types';

export function App() {
  const [token, setToken] = useState<string | null>(() => localStorage.getItem('hwm_token'));
  const [me, setMe] = useState<UserMe | null>(null);
  const [username, setUsername] = useState('operator');
  const [password, setPassword] = useState('operator-dev-pass');
  const [authError, setAuthError] = useState<string | null>(null);
  const [loadingLogin, setLoadingLogin] = useState(false);

  const [backendOffline, setBackendOffline] = useState(false);
  const [devices, setDevices] = useState<DeviceStatus[]>([]);
  const [temps, setTemps] = useState<MeasurementPoint[]>([]);
  const [humids, setHumids] = useState<MeasurementPoint[]>([]);
  const [relays, setRelays] = useState<RelayChannel[]>([]);
  const [control, setControl] = useState<ControlSnapshot | null>(null);
  const [events, setEvents] = useState<AuditEvent[]>([]);

  const [busyRelay, setBusyRelay] = useState<number | null>(null);
  const [actionMsg, setActionMsg] = useState<{ type: 'ok' | 'err'; text: string } | null>(null);

  const [onAboveInput, setOnAboveInput] = useState('30');
  const [offBelowInput, setOffBelowInput] = useState('27');
  const [fanChInput, setFanChInput] = useState('0');
  const [enabledInput, setEnabledInput] = useState(true);
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

  // Load caller profile & capabilities
  useEffect(() => {
    if (!token) return;
    api
      .me(token)
      .then((u) => {
        setMe(u);
        setAuthError(null);
      })
      .catch((err) => {
        if (err instanceof ApiError && err.status === 401) {
          logout(err.code === 'token_expired' ? 'Session expired. Please sign in again.' : 'Unauthorized session.');
        }
      });
  }, [token, logout]);

  // Poll live telemetry & state
  const pollAll = useCallback(async () => {
    if (!token || !me) return;
    try {
      const tasks: Promise<void>[] = [];

      if (hasPerm('read:device_status')) {
        tasks.push(api.devices(token).then(setDevices));
      }
      if (hasPerm('read:sensor')) {
        tasks.push(api.measurements(token, 'sensor', 'temperature_c', 50).then(setTemps));
        tasks.push(api.measurements(token, 'sensor', 'humidity_pct', 50).then(setHumids));
      }
      if (hasPerm('read:relay')) {
        tasks.push(
          api.relays(token).then(setRelays).catch(() => {
            /* keep last known or surface degraded via devices */
          })
        );
      }
      if (hasPerm('read:events')) {
        tasks.push(api.events(token, 30).then(setEvents));
        tasks.push(
          api.getControl(token).then((snap) => {
            setControl(snap);
            setOnAboveInput((prev) => (document.activeElement?.id === 'on_above' ? prev : String(snap.config.on_above)));
            setOffBelowInput((prev) => (document.activeElement?.id === 'off_below' ? prev : String(snap.config.off_below)));
            setFanChInput((prev) => (document.activeElement?.id === 'fan_ch' ? prev : String(snap.config.fan_channel)));
            setEnabledInput((prev) => (document.activeElement?.id === 'auto_enabled' ? prev : snap.config.enabled));
          })
        );
      }

      await Promise.all(tasks);
      setBackendOffline(false);
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        logout('Session expired. Please log in again.');
        return;
      }
      setBackendOffline(true);
    }
  }, [token, me, hasPerm, logout]);

  useEffect(() => {
    if (!token || !me) return;
    pollAll();
    const id = setInterval(pollAll, 2000);
    return () => clearInterval(id);
  }, [token, me, pollAll]);

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
    if (!token || !hasPerm('write:relay')) return;
    setBusyRelay(ch.id);
    setActionMsg(null);
    try {
      const updated = await api.setRelay(token, ch.id, !ch.on);
      setRelays((prev) => prev.map((r) => (r.id === updated.id ? updated : r)));
      setActionMsg({ type: 'ok', text: `Confirmed ${updated.label} -> ${updated.on ? 'ON' : 'OFF'} (read-back verified)` });
      pollAll();
    } catch (err) {
      const msg = err instanceof ApiError ? err.code : 'relay_write_failed';
      setActionMsg({ type: 'err', text: `Relay ${ch.label} failed: ${msg}` });
    } finally {
      setBusyRelay(null);
    }
  };

  const saveControlConfig = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!token || !hasPerm('write:thresholds')) return;
    setSavingCfg(true);
    setActionMsg(null);
    try {
      const snap = await api.putControl(token, {
        enabled: enabledInput,
        on_above: parseFloat(onAboveInput),
        off_below: parseFloat(offBelowInput),
        fan_channel: parseInt(fanChInput, 10),
      });
      setControl(snap);
      setActionMsg({ type: 'ok', text: 'Control thresholds updated and audited.' });
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

          <div className="mt-6 pt-4 border-t border-slate-800 text-xs text-slate-400 space-y-1.5">
            <div className="font-medium text-slate-300">Quick-fill Test Accounts:</div>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => {
                  setUsername('viewer');
                  setPassword('viewer-dev-pass');
                }}
                className="px-2.5 py-1 bg-slate-800 hover:bg-slate-700 rounded text-slate-200"
              >
                viewer (Read-Only)
              </button>
              <button
                type="button"
                onClick={() => {
                  setUsername('operator');
                  setPassword('operator-dev-pass');
                }}
                className="px-2.5 py-1 bg-slate-800 hover:bg-slate-700 rounded text-slate-200"
              >
                operator (Full Control)
              </button>
            </div>
          </div>
        </div>
      </div>
    );
  }

  const canWriteRelay = hasPerm('write:relay');
  const canWriteThresholds = hasPerm('write:thresholds');

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
                    ? `AUTO (${control.fan_on === null ? 'UNKNOWN' : control.fan_on ? 'FAN ON' : 'FAN OFF'})`
                    : 'DISABLED'}
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

        {/* Live Telemetry Charts */}
        {hasPerm('read:sensor') && (
          <section className="grid grid-cols-1 lg:grid-cols-2 gap-4">
            <LiveChart
              title="Ambient Temperature (Modbus Input Reg 0)"
              unit="°C"
              color="#f97316"
              points={temps}
              upperThreshold={control?.config.on_above}
              lowerThreshold={control?.config.off_below}
            />
            <LiveChart
              title="Relative Humidity (Modbus Input Reg 1)"
              unit="%"
              color="#38bdf8"
              points={humids}
            />
          </section>
        )}

        {/* Controls & Threshold Config */}
        <section className="grid grid-cols-1 lg:grid-cols-3 gap-6">
          {/* Relay Card */}
          {hasPerm('read:relay') && (
            <div className="lg:col-span-2 bg-slate-900 border border-slate-800 rounded-xl p-5">
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
                    disabled={!canWriteRelay || busyRelay === ch.id}
                    onClick={() => toggleRelay(ch)}
                    title={!canWriteRelay ? 'Requires write:relay capability' : `Toggle ${ch.label}`}
                    className={`p-3.5 rounded-xl border text-left transition flex flex-col justify-between gap-3 ${
                      ch.on
                        ? 'bg-emerald-500/15 border-emerald-500/40 text-emerald-100'
                        : 'bg-slate-950 border-slate-800 text-slate-300'
                    } ${
                      !canWriteRelay
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
                      </div>
                    </div>
                  </button>
                ))}
              </div>
            </div>
          )}

          {/* Hysteresis Config */}
          {hasPerm('read:events') && (
            <div className="bg-slate-900 border border-slate-800 rounded-xl p-5">
              <div className="flex items-center justify-between mb-4">
                <div className="flex items-center gap-2">
                  <Sliders className="w-5 h-5 text-indigo-400" />
                  <h2 className="font-semibold text-white">Auto Fan Control</h2>
                </div>
                {!canWriteThresholds && (
                  <span className="flex items-center gap-1 text-xs text-amber-300 bg-amber-500/10 border border-amber-500/30 px-2 py-0.5 rounded-full">
                    <Lock className="w-3 h-3" /> Read-Only
                  </span>
                )}
              </div>

              <form onSubmit={saveControlConfig} className="space-y-3">
                <label className="flex items-center gap-2 text-sm text-slate-300">
                  <input
                    id="auto_enabled"
                    type="checkbox"
                    disabled={!canWriteThresholds}
                    checked={enabledInput}
                    onChange={(e) => setEnabledInput(e.target.checked)}
                    className="rounded bg-slate-950 border-slate-700"
                  />
                  Enable Hysteresis Auto-Control
                </label>

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
                    : 'Apply Thresholds'}
                </button>
              </form>
            </div>
          )}
        </section>

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
