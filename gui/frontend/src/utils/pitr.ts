// Shared utilities for PITR-related pages

// Format ISO timestamp as "2026-06-30 07:20:00+00" (UTC)
export function fmtUTC(iso: string): string {
  if (!iso || iso.startsWith('0001')) return '—'
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())} ` +
    `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())}+00`
}

// Parse "YYYY-MM-DD HH:MM:SS+00" (or +HH) into a Date, return null if invalid.
export function parseRestoreTime(s: string): Date | null {
  if (!s) return null
  const norm = s.trim().replace(' ', 'T')
  const iso = norm.endsWith('+00') ? norm + ':00' : norm
  const d = new Date(iso)
  return isNaN(d.getTime()) ? null : d
}
