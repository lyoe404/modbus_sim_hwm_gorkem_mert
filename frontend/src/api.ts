import { AuditEvent, ControlConfig, ControlSnapshot, DeviceStatus, MeasurementPoint, RelayChannel, UserMe } from './types';

export class ApiError extends Error {
  constructor(public status: number, public code: string) {
    super(code);
  }
}

async function req<T>(path: string, token: string | null, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(init?.headers as Record<string, string> || {}),
  };
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }
  const res = await fetch(path, { ...init, headers });
  const text = await res.text();
  let data: any = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = { error: text };
  }
  if (!res.ok) {
    throw new ApiError(res.status, data?.error || `http_${res.status}`);
  }
  return data as T;
}

export const api = {
  login: (username: string, password: string) =>
    req<{ token: string; expires_at: string }>('/api/auth/login', null, {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),
  me: (token: string) => req<UserMe>('/api/auth/me', token),
  devices: (token: string) => req<DeviceStatus[]>('/api/devices', token),
  measurements: (token: string, source: string, metric: string, limit = 60) =>
    req<MeasurementPoint[]>(`/api/measurements?source=${encodeURIComponent(source)}&metric=${encodeURIComponent(metric)}&limit=${limit}`, token),
  relays: (token: string) => req<RelayChannel[]>('/api/relays', token),
  setRelay: (token: string, id: number, on: boolean) =>
    req<RelayChannel>(`/api/relays/${id}`, token, {
      method: 'POST',
      body: JSON.stringify({ on }),
    }),
  getControl: (token: string) => req<ControlSnapshot>('/api/config/control', token),
  putControl: (token: string, cfg: Partial<ControlConfig>) =>
    req<ControlSnapshot>('/api/config/control', token, {
      method: 'PUT',
      body: JSON.stringify(cfg),
    }),
  events: (token: string, limit = 40) => req<AuditEvent[]>(`/api/events?limit=${limit}`, token),
};
