import { useEffect, useState, useCallback, useRef } from 'react'
import {
  InstanceInfo,
  ListInstances,
  StartInstance,
  StopInstance,
  MountInstance,
  UmountInstance,
  FormatInstance,
  OpenConfigFile,
} from '../wailsjs/go'

function StatusDot({ running }: { running: boolean }) {
  return (
    <span
      className={`inline-block w-2 h-2 rounded-full mr-2 ${
        running ? 'bg-green-400' : 'bg-slate-500'
      }`}
    />
  )
}

function PgUrlModal({ url, onClose }: { url: string; onClose: () => void }) {
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    await navigator.clipboard.writeText(url)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  // Close on backdrop click or Escape key
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60"
      onClick={onClose}
    >
      <div
        className="bg-slate-800 border border-slate-600 rounded-lg p-5 w-[480px] max-w-[90vw] shadow-xl"
        onClick={e => e.stopPropagation()}
      >
        <div className="flex items-center justify-between mb-4">
          <span className="text-sm font-semibold text-white">PostgreSQL URL</span>
          <button
            onClick={onClose}
            className="text-slate-400 hover:text-white text-lg leading-none"
          >
            ✕
          </button>
        </div>
        <div className="bg-slate-900 rounded px-3 py-2 text-xs text-green-300 font-mono break-all mb-4 select-all">
          {url}
        </div>
        <div className="flex justify-end">
          <button
            onClick={copy}
            className="px-4 py-1.5 text-xs rounded bg-blue-700 hover:bg-blue-600 transition-colors"
          >
            {copied ? '✓ Copied' : 'Copy'}
          </button>
        </div>
      </div>
    </div>
  )
}

function InstanceCard({ inst, onAction, onClearErr }: { inst: InstanceInfo; onAction: () => void; onClearErr: (clear: () => void) => void }) {
  // Track which operation is in progress: 'start' | 'stop' | 'mount' | 'format' | null
  const [busyOp, setBusyOp] = useState<'start' | 'stop' | 'mount' | 'format' | null>(null)
  const [mountPoint, setMountPoint] = useState(inst.mountPath || '')
  const [userEdited, setUserEdited] = useState(false)
  const [err, setErr] = useState('')
  const [showPgUrl, setShowPgUrl] = useState(false)

  // Sync mountPath from backend when it changes, unless user has typed something
  useEffect(() => {
    if (!userEdited) {
      setMountPoint(inst.mountPath || '')
    }
  }, [inst.mountPath, userEdited])

  // When the instance transitions to running while we're still showing
  // "Starting…", clear the busy state — the backend StartInstance call may
  // still be running post-start setup (PITR stanza, archive config, etc.)
  // that doesn't affect the instance's actual up/down status.
  useEffect(() => {
    if (inst.running && busyOp === 'start') {
      setBusyOp(null)
    }
  }, [inst.running, busyOp])

  // Register the clear function with the parent so manual Refresh can clear err
  useEffect(() => {
    onClearErr(() => setErr(''))
  }, [onClearErr])

  const wrap = async (op: 'start' | 'stop' | 'mount' | 'format', fn: () => Promise<void>, resetEdit = false) => {
    setBusyOp(op)
    setErr('')
    try {
      await fn()
      if (resetEdit) setUserEdited(false)
      onAction()
    } catch (e: unknown) {
      setErr(String(e))
    } finally {
      setBusyOp(null)
    }
  }

  return (
    <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center">
          <StatusDot running={inst.running} />
          <span className="font-semibold text-white">{inst.name}</span>
        </div>
        <button
          onClick={() => setShowPgUrl(true)}
          className="text-xs text-slate-400 bg-slate-700 hover:bg-slate-600 hover:text-white px-2 py-0.5 rounded transition-colors"
        >
          PgURL
        </button>
      </div>

      <div className="text-xs text-slate-400 mb-3 capitalize">{inst.status || 'unknown'}</div>

      {/* Start / Stop */}
      <div className="flex gap-2 mb-3">
        <button
          disabled={busyOp === 'start' || busyOp === 'stop' || inst.running}
          onClick={() => wrap('start', () => StartInstance(inst.name))}
          className="flex-1 px-3 py-1.5 text-xs rounded bg-green-700 hover:bg-green-600 disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex items-center justify-center gap-1.5"
        >
          {busyOp === 'start'
            ? <><span className="animate-spin inline-block">↻</span> Starting…</>
            : 'Start'}
        </button>
        <button
          disabled={busyOp === 'start' || busyOp === 'stop' || !inst.running}
          onClick={() => wrap('stop', () => StopInstance(inst.name))}
          className="flex-1 px-3 py-1.5 text-xs rounded bg-red-800 hover:bg-red-700 disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex items-center justify-center gap-1.5"
        >
          {busyOp === 'stop'
            ? <><span className="animate-spin inline-block">↻</span> Stopping…</>
            : 'Stop'}
        </button>
      </div>

      {/* Format — shown only when running but not yet formatted */}
      {inst.running && !inst.isFormatted && (
        <div className="mb-3">
          <button
            disabled={busyOp === 'format' || busyOp === 'start'}
            onClick={() => wrap('format', () => FormatInstance(inst.name))}
            className="w-full px-3 py-1.5 text-xs rounded bg-yellow-700 hover:bg-yellow-600 disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex items-center justify-center gap-1.5"
          >
            {busyOp === 'format'
              ? <><span className="animate-spin inline-block">↻</span> Formatting…</>
              : '⚠ Format filesystem (required before first mount)'}
          </button>
        </div>
      )}

      {/* Mount / Umount */}
      <div className="flex flex-wrap gap-2">
        <input
          type="text"
          value={mountPoint}
          onChange={(e) => { setMountPoint(e.target.value); setUserEdited(true) }}
          placeholder="~/mnt/aifs  or  Z:\"
          className="flex-1 min-w-[120px] text-xs bg-slate-700 border border-slate-600 rounded px-2 py-1.5 focus:outline-none focus:border-slate-400"
        />
        <button
          disabled={busyOp === 'mount' || !inst.running || !mountPoint || !!inst.mountPath}
          onClick={() => wrap('mount', () => MountInstance(inst.name, mountPoint), true)}
          className="px-3 py-1.5 text-xs rounded bg-blue-700 hover:bg-blue-600 disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex-shrink-0"
        >
          Mount
        </button>
        <button
          disabled={busyOp === 'mount' || !inst.mountPath}
          onClick={() => wrap('mount', () => UmountInstance(inst.mountPath), true)}
          className="px-3 py-1.5 text-xs rounded bg-slate-600 hover:bg-slate-500 disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex-shrink-0"
        >
          Umount
        </button>
      </div>

      {err && <p className="mt-2 text-xs text-red-400 break-words">{err}</p>}

      {showPgUrl && <PgUrlModal url={inst.pgUrl} onClose={() => setShowPgUrl(false)} />}
    </div>
  )
}

interface Props {
  onNewInstance?: () => void
}

export default function Instances({ onNewInstance }: Props) {
  const [instances, setInstances] = useState<InstanceInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [refreshing, setRefreshing] = useState(false)
  // Collect clearErr callbacks from each card; called only on manual Refresh.
  const clearErrCallbacks = useRef<Map<string, () => void>>(new Map())

  const manualRefresh = useCallback(async () => {
    clearErrCallbacks.current.forEach(fn => fn())
    setRefreshing(true)
    try {
      const [result] = await Promise.allSettled([
        ListInstances(),
        new Promise(r => setTimeout(r, 600)),
      ])
      if (result.status === 'fulfilled') {
        setInstances((result.value ?? []).slice().sort((a, b) => a.name.localeCompare(b.name)))
        setErr('')
      } else {
        setErr(String(result.reason))
      }
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  const refresh = useCallback(async () => {
    try {
      const list = await ListInstances()
      setInstances((list ?? []).slice().sort((a, b) => a.name.localeCompare(b.name)))
      setErr('')
    } catch (e: unknown) {
      setErr(String(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    refresh()
    const timer = setInterval(refresh, 5000)
    return () => clearInterval(timer)
  }, [refresh])

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold text-white">Instances</h1>
        <div className="flex gap-2">
          <button
            onClick={manualRefresh}
            disabled={refreshing}
            className="text-xs px-3 py-1.5 rounded bg-slate-700 hover:bg-slate-600 disabled:opacity-60 transition-colors flex items-center gap-1.5"
          >
            <span className={refreshing ? 'animate-spin inline-block' : ''}>↻</span>
            {refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>
      </div>

      {loading && <p className="text-slate-400 text-sm">Loading…</p>}
      {err && <p className="text-red-400 text-sm mb-4">{err}</p>}

      {!loading && instances.length === 0 && !err && (
        <p className="text-slate-400 text-sm">
          No instances found.{' '}
          {onNewInstance ? (
            <button onClick={onNewInstance} className="underline hover:text-white">
              + New Instance
            </button>
          ) : (
            <button onClick={() => OpenConfigFile()} className="underline hover:text-white">
              Open config
            </button>
          )}{' '}
          to add one.
        </p>
      )}

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {instances.map((inst) => (
          <InstanceCard
            key={inst.name}
            inst={inst}
            onAction={refresh}
            onClearErr={(fn) => { clearErrCallbacks.current.set(inst.name, fn) }}
          />
        ))}
      </div>
    </div>
  )
}
