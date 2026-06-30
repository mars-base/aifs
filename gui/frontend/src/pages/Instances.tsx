import { useEffect, useState, useCallback } from 'react'
import {
  InstanceInfo,
  ListInstances,
  StartInstance,
  StopInstance,
  MountInstance,
  UmountInstance,
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

function InstanceCard({ inst, onAction }: { inst: InstanceInfo; onAction: () => void }) {
  const [busy, setBusy] = useState(false)
  const [mountPoint, setMountPoint] = useState(inst.mountPath || '')
  const [userEdited, setUserEdited] = useState(false)
  const [err, setErr] = useState('')

  // Sync mountPath from backend when it changes, unless user has typed something
  useEffect(() => {
    if (!userEdited) {
      setMountPoint(inst.mountPath || '')
    }
  }, [inst.mountPath, userEdited])

  const wrap = async (fn: () => Promise<void>, resetEdit = false) => {
    setBusy(true)
    setErr('')
    try {
      await fn()
      if (resetEdit) setUserEdited(false)
      onAction()
    } catch (e: unknown) {
      setErr(String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="bg-slate-800 rounded-lg p-4 border border-slate-700">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center">
          <StatusDot running={inst.running} />
          <span className="font-semibold text-white">{inst.name}</span>
        </div>
        <span className="text-xs text-slate-400 bg-slate-700 px-2 py-0.5 rounded">
          :{inst.port}
        </span>
      </div>

      <div className="text-xs text-slate-400 mb-3 capitalize">{inst.status || 'unknown'}</div>

      {/* Start / Stop */}
      <div className="flex gap-2 mb-3">
        <button
          disabled={busy || inst.running}
          onClick={() => wrap(() => StartInstance(inst.name))}
          className="flex-1 px-3 py-1.5 text-xs rounded bg-green-700 hover:bg-green-600 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
        >
          Start
        </button>
        <button
          disabled={busy || !inst.running}
          onClick={() => wrap(() => StopInstance(inst.name))}
          className="flex-1 px-3 py-1.5 text-xs rounded bg-red-800 hover:bg-red-700 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
        >
          Stop
        </button>
      </div>

      {/* Mount / Umount */}
      <div className="flex gap-2">
        <input
          type="text"
          value={mountPoint}
          onChange={(e) => { setMountPoint(e.target.value); setUserEdited(true) }}
          placeholder="/mnt/aifs  or  Z:\"
          className="flex-1 text-xs bg-slate-700 border border-slate-600 rounded px-2 py-1.5 focus:outline-none focus:border-slate-400"
        />
        <button
          disabled={busy || !inst.running || !mountPoint || !!inst.mountPath}
          onClick={() => wrap(() => MountInstance(inst.name, mountPoint), true)}
          className="px-3 py-1.5 text-xs rounded bg-blue-700 hover:bg-blue-600 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
        >
          Mount
        </button>
        <button
          disabled={busy || !inst.mountPath}
          onClick={() => wrap(() => UmountInstance(inst.mountPath), true)}
          className="px-3 py-1.5 text-xs rounded bg-slate-600 hover:bg-slate-500 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
        >
          Umount
        </button>
      </div>

      {err && <p className="mt-2 text-xs text-red-400 break-words">{err}</p>}
    </div>
  )
}

export default function Instances() {
  const [instances, setInstances] = useState<InstanceInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

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
            onClick={refresh}
            className="text-xs px-3 py-1.5 rounded bg-slate-700 hover:bg-slate-600 transition-colors"
          >
            Refresh
          </button>
          <button
            onClick={() => OpenConfigFile()}
            className="text-xs px-3 py-1.5 rounded bg-slate-700 hover:bg-slate-600 transition-colors"
          >
            Edit Config
          </button>
        </div>
      </div>

      {loading && <p className="text-slate-400 text-sm">Loading…</p>}
      {err && <p className="text-red-400 text-sm mb-4">{err}</p>}

      {!loading && instances.length === 0 && !err && (
        <p className="text-slate-400 text-sm">
          No instances found.{' '}
          <button onClick={() => OpenConfigFile()} className="underline hover:text-white">
            Open config
          </button>{' '}
          to add one.
        </p>
      )}

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {instances.map((inst) => (
          <InstanceCard key={inst.name} inst={inst} onAction={refresh} />
        ))}
      </div>
    </div>
  )
}
