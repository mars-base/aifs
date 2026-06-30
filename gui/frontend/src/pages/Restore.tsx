import { useEffect, useState, useCallback, useRef } from 'react'
import {
  InstanceInfo,
  Snapshot,
  ListInstances,
  ListSnapshots,
  RestoreInstance,
} from '../wailsjs/go'
import { EventsOn } from '../wailsjs/runtime'
import { fmtUTC, parseRestoreTime } from '../utils/pitr'

const STORAGE_KEY = (instance: string) => `aifs-restore-time:${instance}`
const PAUSED_TIME_KEY = (instance: string) => `aifs-paused-time:${instance}`

export default function Restore() {
  const [instances, setInstances] = useState<InstanceInfo[]>([])
  const [selected, setSelected] = useState('')
  const [snapshots, setSnapshots] = useState<Snapshot[]>([])
  const [restoreTime, setRestoreTime] = useState('')
  const [promote, setPromote] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [success, setSuccess] = useState('')
  const [logs, setLogs] = useState<string[]>([])
  const logsEndRef = useRef<HTMLDivElement>(null)

  // Auto-scroll log panel
  useEffect(() => {
    logsEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [logs])

  // Subscribe to restore-log events from backend
  useEffect(() => {
    const unsub = EventsOn('restore-log', (line: unknown) => {
      setLogs(prev => [...prev, String(line)])
    })
    return () => { if (typeof unsub === 'function') unsub() }
  }, [])

  useEffect(() => {
    ListInstances().then((list) => {
      setInstances(list ?? [])
      if (list?.length > 0 && !selected) setSelected(list[0].name)
    })
  }, [])

  // Restore saved target time when instance changes
  useEffect(() => {
    if (!selected) return
    const saved = localStorage.getItem(STORAGE_KEY(selected)) ?? ''
    setRestoreTime(saved)
    setErr('')
    setSuccess('')
  }, [selected])

  const loadSnapshots = useCallback(async () => {
    if (!selected) return
    try {
      const snaps = await ListSnapshots(selected)
      setSnapshots(snaps ?? [])
    } catch {
      setSnapshots([])
    }
  }, [selected])

  useEffect(() => { loadSnapshots() }, [loadSnapshots])

  // Pre-validate restore time against known snapshot boundaries
  const validationErr = (() => {
    if (!restoreTime) return ''
    const t = parseRestoreTime(restoreTime)
    if (!t) return 'Invalid format — use YYYY-MM-DD HH:MM:SS+00'

    const fulls = snapshots.filter(s => s.type === 'full' && s.stop_time && !s.stop_time.startsWith('0001'))
    if (fulls.length === 0) return 'No full backup available to restore from'

    const earliest = new Date(fulls[0].stop_time)
    if (t < earliest) return `Before earliest restorable point: ${fmtUTC(fulls[0].stop_time)}`

    const ceiling = new Date(Date.now() - 60 * 1000)
    if (t > ceiling) return 'Target time must be at least 1 minute in the past'

    return ''
  })()

  const handleRestore = async () => {
    setBusy(true)
    setErr('')
    setSuccess('')
    setLogs([])
    try {
      await RestoreInstance(selected, restoreTime, promote)
      if (!promote) {
        // Record the time point we just paused at so we can warn on mismatch later
        localStorage.setItem(PAUSED_TIME_KEY(selected), restoreTime)
      } else {
        // After a successful promote, clear the paused marker
        localStorage.removeItem(PAUSED_TIME_KEY(selected))
      }
      setSuccess(`Restore completed. Instance is now in ${promote ? 'read-write (promoted)' : 'read-only (paused)'} mode.`)
      await loadSnapshots()
    } catch (e: unknown) {
      setErr(String(e))
    } finally {
      setBusy(false)
    }
  }

  const earliestFull = snapshots.find(s => s.type === 'full' && !s.stop_time.startsWith('0001'))

  // Warn when promoting with a different time than what was paused
  const pausedTime = selected ? (localStorage.getItem(PAUSED_TIME_KEY(selected)) ?? '') : ''
  const promoteMismatch = promote && pausedTime && restoreTime !== pausedTime

  return (
    <div>
      <h1 className="text-xl font-semibold text-white mb-2">Point-in-Time Restore</h1>
      <div className="flex items-center gap-2 mb-4 px-3 py-2 bg-amber-900/40 border border-amber-600/50 rounded-lg text-xs text-amber-300">
        <span>⏳</span>
        <span>Restore operations may take a long time depending on data size. Please stay on this page until the operation completes.</span>
      </div>
      <p className="text-xs text-slate-400 mb-6">Restore an instance to any point in time after the earliest full backup. Target time is in UTC.</p>

      {/* Instance selector */}
      <div className="flex items-center gap-3 mb-6">
        <label className="text-sm text-slate-400">Instance:</label>
        <div className="relative">
          <select
            value={selected}
            onChange={(e) => { setSelected(e.target.value); setErr(''); setSuccess('') }}
            disabled={busy}
            className="appearance-none bg-slate-700 border border-slate-600 rounded px-3 py-1.5 pr-7 text-sm text-white focus:outline-none focus:border-slate-400 disabled:opacity-50 [&>option]:bg-slate-700 [&>option]:text-white"
          >
            {instances.map((i) => (
              <option key={i.name} value={i.name}>{i.name}</option>
            ))}
          </select>
          <span className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 text-slate-400 text-xs">▾</span>
        </div>
      </div>

      {/* Snapshot reference */}
      {snapshots.length > 0 && (
        <div className="mb-6 bg-slate-800 border border-slate-700 rounded-lg px-4 py-3 text-xs text-slate-400 space-y-1">
          <p className="text-slate-300 font-medium mb-1.5">Available backups</p>
          {snapshots.map(s => (
            <div key={s.name} className="flex items-center gap-3">
              <span className={`px-1.5 py-0.5 rounded text-white ${
                s.type === 'full' ? 'bg-blue-700' : s.type === 'diff' ? 'bg-green-700' : 'bg-orange-700'
              }`}>{s.type}</span>
              <span>{fmtUTC(s.timestamp)}</span>
              {s.stop_time && !s.stop_time.startsWith('0001') && (
                <span className="text-slate-500">→ {fmtUTC(s.stop_time)}</span>
              )}
            </div>
          ))}
          {earliestFull && (
            <p className="mt-2 text-slate-500">Earliest restorable: <span className="text-slate-300">{fmtUTC(earliestFull.stop_time)}</span></p>
          )}
        </div>
      )}

      {/* Restore form */}
      <div className="space-y-5">
        <div>
          <label className="block text-xs text-slate-400 mb-1">Target time (UTC)</label>
          <input
            type="text"
            value={restoreTime}
            onChange={(e) => {
              const v = e.target.value
              setRestoreTime(v)
              if (selected) localStorage.setItem(STORAGE_KEY(selected), v)
              setErr(''); setSuccess('')
            }}
            placeholder="2026-01-01 12:00:00+00"
            disabled={busy}
            className={`bg-slate-700 border rounded px-3 py-1.5 text-sm w-72 focus:outline-none transition-colors disabled:opacity-50 ${
              validationErr ? 'border-red-500 focus:border-red-400' : 'border-slate-600 focus:border-slate-400'
            }`}
          />
          {validationErr && <p className="text-red-400 text-xs mt-1">{validationErr}</p>}
        </div>

        <div>
          <label className="block text-xs text-slate-400 mb-2">After restore</label>
          <div className="flex gap-4 text-sm">
            <label className="flex items-center gap-2 cursor-pointer">
              <input type="radio" checked={!promote} onChange={() => setPromote(false)} disabled={busy} className="accent-blue-500" />
              <span>Pause <span className="text-xs text-slate-400">(read-only, verify first)</span></span>
            </label>
            <label className="flex items-center gap-2 cursor-pointer">
              <input type="radio" checked={promote} onChange={() => setPromote(true)} disabled={busy} className="accent-blue-500" />
              <span>Promote <span className="text-xs text-slate-400">(read-write, fully online)</span></span>
            </label>
          </div>
          {promoteMismatch && (
            <div className="mt-2 flex items-start gap-2 bg-yellow-900/40 border border-yellow-600/50 rounded px-3 py-2 text-xs text-yellow-300">
              <span className="mt-0.5">⚠</span>
              <div>
                <p className="font-medium">Target time differs from Pause restore</p>
                <p className="text-yellow-400/80 mt-0.5">
                  Paused at: <span className="text-yellow-200">{pausedTime}</span><br/>
                  Current:&nbsp;&nbsp;&nbsp; <span className="text-yellow-200">{restoreTime}</span><br/>
                  A full re-restore will be performed instead of a fast promote.
                </p>
              </div>
            </div>
          )}
        </div>

        <button
          disabled={busy || !selected || !restoreTime || !!validationErr}
          onClick={handleRestore}
          className="px-6 py-2 text-sm rounded bg-blue-700 hover:bg-blue-600 disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex items-center gap-2"
        >
          {busy ? <><span className="animate-spin inline-block">↻</span> Restoring…</> : 'Restore'}
        </button>

        {err && <p className="text-red-400 text-sm break-words">{err}</p>}
        {success && <p className="text-green-400 text-sm">{success}</p>}
      </div>

      {/* Restore log panel */}
      {logs.length > 0 && (
        <div className="mt-6">
          <div className="flex items-center justify-between mb-1">
            <span className="text-xs text-slate-400">Restore log</span>
            <button
              onClick={() => setLogs([])}
              disabled={busy}
              className="text-xs text-slate-500 hover:text-slate-300 disabled:opacity-40 transition-colors"
            >
              Clear
            </button>
          </div>
          <div className="bg-slate-900 rounded border border-slate-700 p-3 h-56 overflow-y-auto font-mono text-xs text-green-300 whitespace-pre-wrap">
            {logs.map((line, i) => <span key={i}>{line}</span>)}
            <div ref={logsEndRef} />
          </div>
        </div>
      )}

      {/* Workflow hint */}
      <div className="mt-8 bg-slate-800 border border-slate-700 rounded-lg p-4 text-xs text-slate-400 space-y-1.5">
        <p className="text-slate-300 font-medium">💡 Recommended workflow</p>
        <p>1. Restore with <span className="text-white">Pause (read-only)</span> first.</p>
        <p>2. Go to <span className="text-white">Instances</span> → Umount then re-Mount the instance to get a fresh view.</p>
        <p>3. Browse the mounted files to verify the data looks correct.</p>
        <p>4. If the data is as expected, come back here and restore again with <span className="text-white">Promote (read-write)</span> to bring the instance fully online.</p>
        <p>5. Umount and re-Mount again to get a read-write view.</p>
      </div>
      <p className="mt-3 text-xs text-slate-500">The instance will be stopped and recreated from the nearest snapshot.</p>
    </div>
  )
}
