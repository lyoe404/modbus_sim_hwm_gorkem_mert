import { ThermocoupleSnapshot, SpeedState, AuditEvent, ControlConfig, ControlSnapshot, DeviceStatus, MeasurementPoint, RelayChannel, UserMe } from './types';

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

  // Bound stalled requests, including response-body reads, without losing cleanup cancellation.
  const timeout = AbortSignal.timeout(10000);
  const signal = init?.signal ? AbortSignal.any([init.signal, timeout]) : timeout;
  let res: Response;
  try {
    res = await fetch(path, { ...init, headers, signal });
  } catch {
    throw new ApiError(0, 'backend_unreachable'); // ağ hatası
  }

  const text = await res.text();
  let data: any = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = null; // HTML'i hata koduna çevirme
  }

  if (!res.ok) {
    const gateway = [502, 503, 504].includes(res.status);
    throw new ApiError(
      res.status,
      data?.error ?? (gateway ? 'backend_unreachable' : `http_${res.status}`),
    );
  }
  return data as T;
}

export const api = {
 thermocouple: (token:string,signal?:AbortSignal)=>req<ThermocoupleSnapshot>('/api/thermocouple',token,{signal}),
 speed: (token:string,signal?:AbortSignal)=>req<SpeedState>('/api/fan/speed',token,{signal}),
 setSpeed: (token:string,percent:number)=>req<SpeedState>('/api/fan/speed',token,{method:'POST',body:JSON.stringify({percent})}),
 reset: (token:string,target:string)=>req<{target:string;state:string}>('/api/reset',token,{method:'POST',body:JSON.stringify({target})}),
  login: (username: string, password: string) =>
    req<{ token: string; expires_at: string }>('/api/auth/login', null, {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),
  health: (signal?: AbortSignal) => req<{ status: string }>('/healthz', null, { signal, cache: 'no-store' }),
  me: (token: string, signal?: AbortSignal) => req<UserMe>('/api/auth/me', token, { signal }),
  devices: (token: string, signal?: AbortSignal) => req<DeviceStatus[]>('/api/devices', token, { signal }),
  measurements: (token: string, source: string, metric: string, limit = 60, signal?: AbortSignal) =>
    req<MeasurementPoint[]>(`/api/measurements?source=${encodeURIComponent(source)}&metric=${encodeURIComponent(metric)}&limit=${limit}`, token, { signal }),
  relays: (token: string, signal?: AbortSignal) => req<RelayChannel[]>('/api/relays', token, { signal }),
  setRelay: (token: string, id: number, on: boolean) =>
    req<RelayChannel>(`/api/relays/${id}`, token, {
      method: 'POST',
      body: JSON.stringify({ on }),
    }),
  getControl: (token: string, signal?: AbortSignal) => req<ControlSnapshot>('/api/config/control', token, { signal }),
  putControl: (token: string, cfg: Partial<ControlConfig>) =>
    req<ControlSnapshot>('/api/config/control', token, {
      method: 'PUT',
      body: JSON.stringify(cfg),
    }),
  events: (token: string, limit = 40, signal?: AbortSignal) => req<AuditEvent[]>(`/api/events?limit=${limit}`, token, { signal }),
};
