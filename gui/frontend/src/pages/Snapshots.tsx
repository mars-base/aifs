import { useEffect, useState, useCallback, useRef } from 'react'
import {
  InstanceInfo,
  Snapshot,
  ListInstances,
  ListSnapshots,
  CreateSnapshot,
  DeleteSnapshot,
  ShowConfirm,
  ShowAlert,
} from '../wailsjs/go'
import { EventsOn } from '../wailsjs/runtime'
import { fmtUTC } from '../utils/pitr'

const SNAP_TYPE_COLOR: Record<string, string> = {
  full: 'bg-blue-700',
  diff: 'bg-green-700',
  incr: 'bg-orange-700',
}

export default function Snapshots() {
  const [instances, setInstances] = useState<InstanceInfo[]>([])
  const [selected, setSelected] = useState('')
  const [snapshots, setSnapshots] = useState<Snapshot[]>([])
  const [busy, setBusy] = useState(false)
  const [backingUp, setBackingUp] = useState<string | null>(null) // 'full' | 'diff' | null
  const [snapErr, setSnapErr] = useState('')
  const [logs, setLogs] = useState<string[]>([])
  const logsEndRef = useRef<HTMLDivElement>(null)

  // Auto-scroll log panel to bottom on new lines
  useEffect(() => {
    logsEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [logs])

  // Subscribe to real-time backup log events from the backend
  useEffect(() => {
    const handler = (line: unknown) => {
      setLogs(prev => [...prev, String(line)])
    }
    const unsub = EventsOn('snapshot-log', handler)
    return () => { if (typeof unsub === 'function') unsub() }
  }, [])

  useEffect(() => {
    ListInstances().then((list) => {
      setInstances(list ?? [])
      if (list?.length > 0 && !selected) setSelected(list[0].name)
    })
  }, [])

  const loadSnapshots = useCallback(async () => {
    if (!selected) return
    setBusy(true)
    setSnapErr('')
    try {
      const snaps = await ListSnapshots(selected)
      setSnapshots(snaps ?? [])
    } catch (e: unknown) {
      setSnapErr(String(e))
      setSnapshots([])
    } finally {
      setBusy(false)
    }
  }, [selected])

  useEffect(() => {
    loadSnapshots()
  }, [loadSnapshots])

  const handleDelete = async (snap: Snapshot) => {
    if (snap.type === 'full') {
      const fulls = snapshots.filter(s => s.type === 'full')
      // Guard: only one full backup — cannot delete it
      if (fulls.length <= 1) {
        await ShowAlert('Cannot Delete', 'Cannot delete the only full backup.\nAt least one full backup must be retained to support point-in-time recovery.')
        return
      }
      // Guard: disallow deleting the newest full backup
      const newestFull = fulls.reduce((a, b) =>
        new Date(a.timestamp) > new Date(b.timestamp) ? a : b
      )
      if (snap.name === newestFull.name) {
        await ShowAlert(
          'Cannot Delete',
          'Cannot delete the newest full backup.\n\nTo free up storage, delete an older full backup instead — ' +
          'pgBackRest will automatically clean up the WAL archive segments that are no longer needed.'
        )
        return
      }
    }
    const label = `${snap.type} — ${fmtUTC(snap.timestamp)}`
    const ok = await ShowConfirm('Delete Snapshot', `Delete snapshot "${snap.name}" (${label})?\n\nThis action cannot be undone.`)
    if (!ok) return
    wrap(() => DeleteSnapshot(selected, snap.name))
  }

  const wrap = async (fn: () => Promise<void>, clearLogs = false, snapType?: string) => {
    setBusy(true)
    if (snapType) setBackingUp(snapType)
    setSnapErr('')
    if (clearLogs) setLogs([])
    try {
      await fn()
      await loadSnapshots()
    } catch (e: unknown) {
      setSnapErr(String(e))
    } finally {
      setBusy(false)
      setBackingUp(null)
    }
  }

  return (
    <div>
      <h1 className="text-xl font-semibold text-white mb-2">Snapshots</h1>
      <div className="flex items-center gap-2 mb-6 px-3 py-2 bg-amber-900/40 border border-amber-600/50 rounded-lg text-xs text-amber-300">
        <span>⏳</span>
        <span>Backup and delete operations may take a long time depending on data size. Please stay on this page until the operation completes.</span>
      </div>

      {/* Instance selector */}
      <div className="flex items-center gap-3 mb-6">
        <label className="text-sm text-slate-400">Instance:</label>
        <div className="relative">
          <select
            value={selected}
            onChange={(e) => setSelected(e.target.value)}
            className="appearance-none bg-slate-700 border border-slate-600 rounded px-3 py-1.5 pr-7 text-sm text-white focus:outline-none focus:border-slate-400 [&>option]:bg-slate-700 [&>option]:text-white"
          >
            {instances.map((i) => (
              <option key={i.name} value={i.name}>{i.name}</option>
            ))}
          </select>
          <span className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 text-slate-400 text-xs">▾</span>
        </div>
        <button
          onClick={loadSnapshots}
          disabled={busy}
          className="text-xs px-3 py-1.5 rounded bg-slate-700 hover:bg-slate-600 disabled:opacity-40 transition-colors"
        >
          Refresh
        </button>
      </div>

      {/* Create snapshot buttons */}
      <div className="flex gap-2 mb-6">
        <button
          disabled={busy || !selected}
          onClick={() => wrap(() => CreateSnapshot(selected, 'full'), true, 'full')}
          className={`px-4 py-1.5 text-xs rounded ${SNAP_TYPE_COLOR['full']} hover:opacity-80 disabled:opacity-40 disabled:cursor-not-allowed transition-opacity capitalize flex items-center gap-1.5`}
        >
          {backingUp === 'full' ? <><span className="animate-spin inline-block">↻</span> Backing up…</> : '+ full'}
        </button>
        <button
          disabled={busy || !selected || !snapshots.some(s => s.type === 'full')}
          title={snapshots.some(s => s.type === 'full') ? '' : 'Requires a full backup first'}
          onClick={() => wrap(() => CreateSnapshot(selected, 'diff'), true, 'diff')}
          className={`px-4 py-1.5 text-xs rounded ${SNAP_TYPE_COLOR['diff']} hover:opacity-80 disabled:opacity-40 disabled:cursor-not-allowed transition-opacity capitalize flex items-center gap-1.5`}
        >
          {backingUp === 'diff' ? <><span className="animate-spin inline-block">↻</span> Backing up…</> : '+ diff'}
        </button>
      </div>

      {snapErr && <p className="text-red-400 text-sm mb-4 break-words">{snapErr}</p>}

      {/* Snapshot list */}
      {snapshots.length === 0 && !busy && (
        <p className="text-slate-400 text-sm mb-6">No snapshots found.</p>
      )}
      {snapshots.length > 0 && (
        <div className="mb-8 space-y-2">
          {snapshots.map((s) => (
            <div
              key={s.name}
              className="flex items-center justify-between bg-slate-800 rounded-lg px-4 py-3 border border-slate-700"
            >
              <div className="flex items-center gap-3">
                <span
                  className={`text-xs px-2 py-0.5 rounded ${SNAP_TYPE_COLOR[s.type] ?? 'bg-slate-600'} capitalize`}
                >
                  {s.type}
                </span>
                <div>
                  <div className="text-sm font-medium text-white">{s.name}</div>
                  <div className="text-xs text-slate-400">
                    Start: {fmtUTC(s.timestamp)}
                    {s.stop_time && s.stop_time !== '0001-01-01T00:00:00Z' && (
                      <> &nbsp;·&nbsp; Stop: {fmtUTC(s.stop_time)}</>
                    )}
                  </div>
                </div>
              </div>
              <button
                disabled={busy}
                onClick={() => handleDelete(s)}
                className="text-xs px-3 py-1 rounded bg-red-900 hover:bg-red-800 disabled:opacity-40 transition-colors"
              >
                Delete
              </button>
            </div>
          ))}
        </div>
      )}

      {/* Backup log panel — shown only when there are log lines */}
      {logs.length > 0 && (
        <div className="mb-6">
          <div className="flex items-center justify-between mb-1">
            <span className="text-xs text-slate-400">Backup log</span>
            <button
              onClick={() => setLogs([])}
              className="text-xs text-slate-500 hover:text-slate-300 transition-colors"
            >
              Clear
            </button>
          </div>
          <div className="bg-slate-900 rounded border border-slate-700 p-3 h-48 overflow-y-auto font-mono text-xs text-green-300 whitespace-pre-wrap">
            {logs.map((line, i) => <span key={i}>{line}</span>)}
            <div ref={logsEndRef} />
          </div>
        </div>
      )}
    </div>
  )
}
