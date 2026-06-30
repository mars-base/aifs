import { useEffect, useState, useCallback } from 'react'
import {
  InstanceInfo,
  Snapshot,
  ListInstances,
  ListSnapshots,
  CreateSnapshot,
  DeleteSnapshot,
  RestoreInstance,
} from '../wailsjs/go'

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
  const [err, setErr] = useState('')

  // PITR restore form
  const [restoreTime, setRestoreTime] = useState('')
  const [promote, setPromote] = useState(true)

  useEffect(() => {
    ListInstances().then((list) => {
      setInstances(list ?? [])
      if (list?.length > 0 && !selected) {
        setSelected(list[0].name)
      }
    })
  }, [])

  const loadSnapshots = useCallback(async () => {
    if (!selected) return
    setBusy(true)
    setErr('')
    try {
      const snaps = await ListSnapshots(selected)
      setSnapshots(snaps ?? [])
    } catch (e: unknown) {
      setErr(String(e))
      setSnapshots([])
    } finally {
      setBusy(false)
    }
  }, [selected])

  useEffect(() => {
    loadSnapshots()
  }, [loadSnapshots])

  const wrap = async (fn: () => Promise<void>) => {
    setBusy(true)
    setErr('')
    try {
      await fn()
      await loadSnapshots()
    } catch (e: unknown) {
      setErr(String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <h1 className="text-xl font-semibold text-white mb-6">Snapshots</h1>

      {/* Instance selector */}
      <div className="flex items-center gap-3 mb-6">
        <label className="text-sm text-slate-400">Instance:</label>
        <select
          value={selected}
          onChange={(e) => setSelected(e.target.value)}
          className="bg-slate-700 border border-slate-600 rounded px-3 py-1.5 text-sm focus:outline-none focus:border-slate-400"
        >
          {instances.map((i) => (
            <option key={i.name} value={i.name}>
              {i.name}
            </option>
          ))}
        </select>
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
        {(['full', 'diff', 'incr'] as const).map((t) => (
          <button
            key={t}
            disabled={busy || !selected}
            onClick={() => wrap(() => CreateSnapshot(selected, t))}
            className={`px-4 py-1.5 text-xs rounded ${SNAP_TYPE_COLOR[t]} hover:opacity-80 disabled:opacity-40 disabled:cursor-not-allowed transition-opacity capitalize`}
          >
            + {t}
          </button>
        ))}
      </div>

      {err && <p className="text-red-400 text-sm mb-4 break-words">{err}</p>}

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
                    {new Date(s.timestamp).toLocaleString()} — {(s.size / 1024 / 1024).toFixed(1)} MiB
                  </div>
                </div>
              </div>
              <button
                disabled={busy}
                onClick={() => wrap(() => DeleteSnapshot(selected, s.name))}
                className="text-xs px-3 py-1 rounded bg-red-900 hover:bg-red-800 disabled:opacity-40 transition-colors"
              >
                Delete
              </button>
            </div>
          ))}
        </div>
      )}

      {/* PITR restore */}
      <div className="border-t border-slate-700 pt-6">
        <h2 className="text-base font-semibold text-white mb-4">Point-in-Time Restore</h2>
        <div className="flex flex-wrap gap-4 items-end">
          <div>
            <label className="block text-xs text-slate-400 mb-1">Target time</label>
            <input
              type="text"
              value={restoreTime}
              onChange={(e) => setRestoreTime(e.target.value)}
              placeholder="2024-01-01 12:00:00+00"
              className="bg-slate-700 border border-slate-600 rounded px-3 py-1.5 text-sm w-64 focus:outline-none focus:border-slate-400"
            />
          </div>
          <div>
            <label className="block text-xs text-slate-400 mb-1">After restore</label>
            <div className="flex gap-3 text-sm">
              <label className="flex items-center gap-1.5 cursor-pointer">
                <input
                  type="radio"
                  checked={promote}
                  onChange={() => setPromote(true)}
                  className="accent-blue-500"
                />
                Promote (read-write)
              </label>
              <label className="flex items-center gap-1.5 cursor-pointer">
                <input
                  type="radio"
                  checked={!promote}
                  onChange={() => setPromote(false)}
                  className="accent-blue-500"
                />
                Pause (read-only)
              </label>
            </div>
          </div>
          <button
            disabled={busy || !selected || !restoreTime}
            onClick={() => wrap(() => RestoreInstance(selected, restoreTime, promote))}
            className="px-5 py-1.5 text-sm rounded bg-blue-700 hover:bg-blue-600 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
          >
            Restore
          </button>
        </div>
        <p className="mt-2 text-xs text-slate-500">
          Format: <code>YYYY-MM-DD HH:MM:SS+00</code>. The instance will be stopped and recreated
          from the nearest snapshot.
        </p>
      </div>
    </div>
  )
}
