export interface UserMe {
  username: string;
  role: string;
  perms: string[];
}

export interface DeviceStatus {
  name: string;
  state: 'online' | 'degraded' | 'offline' | string;
  consecutive_failures: number;
  last_seen?: string;
  last_error?: string;
}

export interface MeasurementPoint {
  source: string;
  metric: string;
  value: number;
  at: string;
}

export interface RelayChannel {
  id: number;
  label: string;
  on: boolean;
}

export interface ControlConfig {
  enabled: boolean;
  on_above: number;
  off_below: number;
  fan_channel: number;
}

export interface ControlSnapshot {
  config: ControlConfig;
  fan_on: boolean | null;
  suspended: boolean;
}

export interface AuditEvent {
  id?: number;
  kind: string;
  source: string;
  message: string;
  actor?: string;
  at: string;
}

export interface ThermocoupleChannel { source: string; status: 'live'|'fault'|'unknown'; temperature_c: number|null; last_valid_at: string }
export interface ThermocoupleSnapshot { channels: ThermocoupleChannel[]; all_channels_faulted: boolean }
export interface SpeedState { command_pct: number; feedback_rpm: number; mismatch: boolean }
