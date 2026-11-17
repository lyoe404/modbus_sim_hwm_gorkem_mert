import React from 'react';
import { MeasurementPoint } from './types';

interface Props {
  title: string;
  unit: string;
  color: string;
  points: MeasurementPoint[];
  upperThreshold?: number;
  lowerThreshold?: number;
}

export const LiveChart: React.FC<Props> = ({ title, unit, color, points, upperThreshold, lowerThreshold }) => {
  const sorted = [...points].sort((a, b) => new Date(a.at).getTime() - new Date(b.at).getTime());
  const latest = sorted[sorted.length - 1];

  const vals = sorted.map((p) => p.value);
  if (upperThreshold !== undefined) vals.push(upperThreshold);
  if (lowerThreshold !== undefined) vals.push(lowerThreshold);

  const min = vals.length ? Math.min(...vals) - 1 : 0;
  const max = vals.length ? Math.max(...vals) + 1 : 100;
  const range = max - min || 1;

  const width = 560;
  const height = 160;
  const pad = 12;

  const toY = (v: number) => height - pad - ((v - min) / range) * (height - pad * 2);
  const toX = (i: number) =>
    sorted.length <= 1 ? width / 2 : pad + (i / (sorted.length - 1)) * (width - pad * 2);

  const pathD = sorted
    .map((p, i) => `${i === 0 ? 'M' : 'L'} ${toX(i).toFixed(1)} ${toY(p.value).toFixed(1)}`)
    .join(' ');

  return (
    <div className="bg-slate-900 border border-slate-800 rounded-xl p-4 flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-sm font-medium text-slate-400">{title}</h3>
          <div className="text-2xl font-bold text-slate-100 mt-0.5">
            {latest ? `${latest.value.toFixed(1)} ${unit}` : `-- ${unit}`}
          </div>
        </div>
        {latest && (
          <span className="text-xs text-slate-500">
            Updated {new Date(latest.at).toLocaleTimeString()}
          </span>
        )}
      </div>

      <div className="w-full overflow-hidden">
        <svg viewBox={`0 0 ${width} ${height}`} className="w-full h-40 bg-slate-950/60 rounded-lg border border-slate-800/80">
          {upperThreshold !== undefined && (
            <line
              x1={pad}
              x2={width - pad}
              y1={toY(upperThreshold)}
              y2={toY(upperThreshold)}
              stroke="#ef4444"
              strokeDasharray="4 4"
              strokeWidth="1"
            />
          )}
          {lowerThreshold !== undefined && (
            <line
              x1={pad}
              x2={width - pad}
              y1={toY(lowerThreshold)}
              y2={toY(lowerThreshold)}
              stroke="#3b82f6"
              strokeDasharray="4 4"
              strokeWidth="1"
            />
          )}
          {sorted.length > 1 && (
            <path d={pathD} fill="none" stroke={color} strokeWidth="2.5" strokeLinecap="round" />
          )}
          {sorted.map((p, i) => (
            <circle key={i} cx={toX(i)} cy={toY(p.value)} r="2.5" fill={color} />
          ))}
        </svg>
      </div>
      {(upperThreshold !== undefined || lowerThreshold !== undefined) && (
        <div className="flex gap-4 text-xs text-slate-400">
          {upperThreshold !== undefined && (
            <span className="flex items-center gap-1.5">
              <span className="w-2.5 h-0.5 bg-red-500 inline-block" /> On Above: {upperThreshold}{unit}
            </span>
          )}
          {lowerThreshold !== undefined && (
            <span className="flex items-center gap-1.5">
              <span className="w-2.5 h-0.5 bg-blue-500 inline-block" /> Off Below: {lowerThreshold}{unit}
            </span>
          )}
        </div>
      )}
    </div>
  );
};
