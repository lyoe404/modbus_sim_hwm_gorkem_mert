import React, { useEffect, useState } from 'react';
import { Check, Fan, Loader2, Lock, RotateCcw, Thermometer, X } from 'lucide-react';
import { api, ApiError } from './api';
import { ControlSnapshot, DeviceStatus, SpeedState, ThermocoupleSnapshot } from './types';

interface Props {
  token: string;
  hasPerm: (permission: string) => boolean;
  thermocouple: ThermocoupleSnapshot | null;
  speed: SpeedState | null;
  control: ControlSnapshot | null;
  devices: DeviceStatus[];
  resetting: Record<string, boolean>;
  thermoLive: boolean;
  speedLive: boolean;
  backendOffline: boolean;
  onControl: (snapshot: ControlSnapshot) => void;
  onSpeed: (state: SpeedState | null) => void;
  onReset: (target: string) => Promise<void>;
}

type NoticeValue = { kind: 'success' | 'error'; text: string } | null;
const targets = [
  { id: 'temp-humidity', name: 'Ambient sensor', source: 'sensor', detail: 'Temperature & humidity' },
  { id: 'relay', name: 'Relay card', source: 'relay', detail: 'Fan & output channels' },
  { id: 'thermocouple', name: 'Thermocouple', source: 'thermocouple', detail: 'Four temperature probes' },
  { id: 'speed-pwm', name: 'PWM controller', source: 'fan_speed', detail: 'Fan speed & RPM feedback' },
];

function errorMessage(error: unknown): string {
  if (!(error instanceof ApiError)) return 'The request could not be completed. Please try again.';
  if (error.status === 409) return 'The fan is in AUTO mode. Switch to MANUAL before setting a speed.';
  if (error.status === 403) return 'Your account does not have permission for this action.';
  if (error.status === 401) return 'Your session has expired. Please sign in again.';
  if (error.code === 'speed_write_failed') return 'The speed command could not be verified. Check the device connection and try again.';
  if (error.code === 'reset_failed') return 'The reset service did not confirm the request. Check the device status before retrying.';
  if (error.status === 0) return 'The server could not be reached. Check your connection and try again.';
  return 'The device is unavailable or recovering. Wait for a fresh reading and try again.';
}

function Notice({ value }: { value: NoticeValue }) {
  if (!value) return null;
  return <div role={value.kind === 'error' ? 'alert' : 'status'} className={`device-notice ${value.kind}`}>
    {value.kind === 'success' ? <Check size={16} /> : <X size={16} />}
    <span>{value.text}</span>
  </div>;
}

function Status({ state }: { state: string }) {
  return <span className={`device-status ${state}`}><span />{state.replace(/_/g, ' ')}</span>;
}

export function ExtendedPanels(props: Props) {
  const { token, hasPerm, thermocouple, speed, control, devices, resetting, thermoLive, speedLive,
    backendOffline, onControl, onSpeed, onReset } = props;
  const [draft, setDraft] = useState(0);
  const [dirty, setDirty] = useState(false);
  const [speedBusy, setSpeedBusy] = useState(false);
  const [modeBusy, setModeBusy] = useState(false);
  const [speedNotice, setSpeedNotice] = useState<NoticeValue>(null);
  const [selectedTarget, setSelectedTarget] = useState<string | null>(null);
  const [resetBusy, setResetBusy] = useState<string | null>(null);
  const [resetNotice, setResetNotice] = useState<NoticeValue>(null);
  const auto = control?.config.enabled;
  const currentSpeed = speedLive ? speed : null;
  const speedAvailable = Boolean(currentSpeed) && !backendOffline && !resetting['speed-pwm'];
  const canSetSpeed = hasPerm('write:fan_speed') && auto === false && speedAvailable && !speedBusy && !modeBusy;

  useEffect(() => {
    if (!dirty && !speedBusy && speed) setDraft(speed.command_pct);
  }, [speed, dirty, speedBusy]);

  const changeMode = async (enabled: boolean) => {
    if (!hasPerm('write:thresholds') || !control || modeBusy || speedBusy || enabled === auto) return;
    setModeBusy(true);
    setSpeedNotice(null);
    try {
      const snapshot = await api.putControl(token, { enabled });
      onControl(snapshot);
      setDirty(false);
      setSpeedNotice({ kind: 'success', text: enabled ? 'AUTO mode enabled. Temperature now controls fan speed.' : 'MANUAL mode enabled. Choose a speed and apply it below.' });
    } catch (error) {
      setSpeedNotice({ kind: 'error', text: errorMessage(error) });
    } finally { setModeBusy(false); }
  };

  const applySpeed = async () => {
    if (!canSetSpeed) return;
    setSpeedBusy(true);
    setSpeedNotice(null);
    try {
      const actual = await api.setSpeed(token, draft);
      onSpeed(actual);
      setDraft(actual.command_pct);
      setDirty(false);
      setSpeedNotice({ kind: 'success', text: `Speed set to ${actual.command_pct}%. Device read-back confirmed.` });
    } catch (error) {
      onSpeed(null);
      setSpeedNotice({ kind: 'error', text: errorMessage(error) });
    } finally { setSpeedBusy(false); }
  };

  const confirmReset = async () => {
    if (!selectedTarget || resetBusy || backendOffline || !hasPerm('write:reset')) return;
    const target = selectedTarget;
    setResetBusy(target);
    setResetNotice(null);
    try {
      await onReset(target);
      setSelectedTarget(null);
      setResetNotice({ kind: 'success', text: `Reset accepted for ${target}. Live values will resume after verified recovery.` });
    } catch (error) {
      setResetNotice({ kind: 'error', text: errorMessage(error) });
    } finally { setResetBusy(null); }
  };

  return <div className="space-y-6">
    <section className="grid grid-cols-1 xl:grid-cols-2 gap-6">
      {hasPerm('read:thermocouple') && <article className="device-panel">
        <header className="device-panel-header">
          <div className="device-heading"><span className="device-icon warm"><Thermometer size={20} /></span><div><h2>Thermocouple</h2><p>Four independent temperature channels</p></div></div>
          <Status state={thermoLive && thermocouple ? 'connected' : 'unavailable'} />
        </header>
        <div className="grid grid-cols-2 gap-3">
          {Array.from({ length: 4 }, (_, index) => {
            const channel = thermocouple?.channels[index];
            const state = thermoLive ? channel?.status ?? 'unknown' : 'unknown';
            const valid = state === 'live' && channel?.temperature_c != null;
            const lastValid = channel && Date.parse(channel.last_valid_at) > 0 ? new Date(channel.last_valid_at).toLocaleTimeString() : null;
            return <div className={`probe-card ${state}`} key={index}>
              <div className="flex items-center justify-between gap-2"><span className="probe-label">CHANNEL 0{index + 1}</span><Status state={state} /></div>
              <div className="probe-value">{valid ? channel.temperature_c!.toFixed(1) : '\u2014'}<span>&deg;C</span></div>
              <p>{state === 'fault' ? 'Probe fault detected' : state === 'live' ? 'Receiving live measurements' : resetting.thermocouple ? 'Waiting for recovery' : 'Waiting for a valid reading'}</p>
              <small>Last valid <span>{lastValid ?? '\u2014'}</span></small>
            </div>;
          })}
        </div>
        {thermocouple?.all_channels_faulted && thermoLive && <p className="device-hint text-amber-300">All probes report a fault. The module is still connected.</p>}
      </article>}

      {hasPerm('read:fan_speed') && <article className="device-panel">
        <header className="device-panel-header">
          <div className="device-heading"><span className="device-icon"><Fan size={20} /></span><div><h2>PWM fan speed</h2><p>Command output & measured feedback</p></div></div>
          <Status state={currentSpeed ? 'live' : 'unavailable'} />
        </header>
        <div className="pwm-readings">
          <div className="pwm-dial" aria-label={currentSpeed ? `Command ${currentSpeed.command_pct}%` : 'Speed unavailable'}>
            <svg viewBox="0 0 120 120" aria-hidden="true"><circle cx="60" cy="60" r="50" /><circle className="dial-fill" cx="60" cy="60" r="50" strokeDasharray={`${(currentSpeed?.command_pct ?? 0) * Math.PI} ${100 * Math.PI}`} /></svg>
            <div><strong>{currentSpeed ? currentSpeed.command_pct : '\u2014'}<small>%</small></strong><span>COMMAND</span></div>
          </div>
          <div className="flex-1 space-y-4">
            <div><span className="device-eyebrow">FEEDBACK SPEED</span><p className="rpm-value">{currentSpeed ? currentSpeed.feedback_rpm.toLocaleString() : '\u2014'} <span>RPM</span></p></div>
            <div className="mode-picker" aria-label="Fan control mode">
              {[true, false].map(enabled => <button type="button" key={String(enabled)} aria-pressed={auto === enabled} className={auto === enabled ? 'selected' : ''} disabled={!control || !hasPerm('write:thresholds') || modeBusy || speedBusy || backendOffline} onClick={() => changeMode(enabled)}>{modeBusy && auto !== enabled ? <Loader2 size={12} className="animate-spin" /> : null}{enabled ? 'AUTO' : 'MANUAL'}</button>)}
            </div>
          </div>
        </div>
        <div className="manual-speed">
          <div className="flex items-center justify-between"><label htmlFor="pwm-speed">Manual output</label><span className="font-mono text-indigo-300">{draft}%</span></div>
          <input id="pwm-speed" aria-label="Manual PWM speed" type="range" min="0" max="100" step="1" value={draft} disabled={!canSetSpeed} onChange={event => { setDraft(Number(event.target.value)); setDirty(true); setSpeedNotice(null); }} />
          <div className="flex justify-between text-xs text-slate-500"><span>0% / Stop</span><span>100% / Full speed</span></div>
          <div className="flex flex-wrap items-center justify-between gap-3 mt-4">
            <p className="device-hint flex-1">{!hasPerm('write:fan_speed') ? 'Read-only access. An operator can change fan speed.' : auto === true ? 'Switch to MANUAL to set the output. AUTO follows ambient temperature.' : auto === undefined ? 'Waiting for control mode.' : !speedAvailable ? 'Controls resume when live feedback is available.' : dirty ? 'Unsaved output. Apply to send it to the controller.' : 'Choose an output and apply it to the controller.'}</p>
            {hasPerm('write:fan_speed') && <button type="button" className="device-button primary" disabled={!canSetSpeed} onClick={applySpeed}>{speedBusy ? <Loader2 size={15} className="animate-spin" /> : <Check size={15} />}{speedBusy ? 'Applying...' : 'Apply speed'}</button>}
          </div>
        </div>
        {currentSpeed?.mismatch && <p className="device-notice warning">RPM feedback differs from the command after the settling period. Check the fan or feedback signal.</p>}
        <Notice value={speedNotice} />
      </article>}
    </section>

    {hasPerm('write:reset') && <section className="device-panel">
      <header className="device-panel-header">
        <div className="device-heading"><span className="device-icon"><RotateCcw size={20} /></span><div><h2>Remote reset</h2><p>Restart a device and monitor its recovery</p></div></div>
        <span className="hidden sm:flex items-center gap-1.5 text-xs text-slate-500"><Lock size={13} />Operator control</span>
      </header>
      <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-3">
        {targets.map(target => {
          const device = devices.find(item => item.name === target.source);
          const state = device?.state === 'recovery_failed' ? 'recovery_failed' : resetting[target.id] ? 'recovering' : device?.state ?? 'unknown';
          const waiting = state === 'recovering' || (resetting[target.id] && state !== 'recovery_failed');
          return <div className={`reset-device ${selectedTarget === target.id ? 'selected' : ''}`} key={target.id}>
            <Status state={state} />
            <h3>{target.name}</h3><p>{target.detail}</p>
            <button type="button" className="device-button secondary w-full" disabled={Boolean(resetBusy) || backendOffline || waiting} onClick={() => { setSelectedTarget(target.id); setResetNotice(null); }}><RotateCcw size={14} className={waiting ? 'animate-spin' : ''} />{waiting ? 'Recovering...' : state === 'recovery_failed' ? 'Retry reset' : 'Reset device'}</button>
          </div>;
        })}
      </div>
      {selectedTarget && <div className="reset-confirm" role="group" aria-label="Confirm device reset">
        <div><h3>Reset {selectedTarget}?</h3><p>Live readings and controls for this device will be unavailable until recovery is verified.</p></div>
        <div className="flex items-center gap-2 shrink-0">
          <button type="button" className="device-button secondary" disabled={Boolean(resetBusy)} onClick={() => setSelectedTarget(null)}>Cancel</button>
          <button type="button" className="device-button danger" disabled={Boolean(resetBusy) || backendOffline} onClick={confirmReset}>{resetBusy && <Loader2 size={14} className="animate-spin" />}{resetBusy ? 'Resetting...' : 'Confirm reset'}</button>
        </div>
      </div>}
      <Notice value={resetNotice} />
    </section>}
  </div>;
}


