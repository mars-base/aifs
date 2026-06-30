import { useEffect, useState } from 'react'
import { ConfigStatus, GetConfigStatus, InitConfig } from '../wailsjs/go'

interface Props {
  onInitialized: () => void
}

export default function Setup({ onInitialized }: Props) {
  const [status, setStatus] = useState<ConfigStatus | null>(null)
  const [baseDir, setBaseDir] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [success, setSuccess] = useState('')

  const loadStatus = () => {
    GetConfigStatus().then(s => setStatus(s))
  }

  useEffect(() => { loadStatus() }, [])

  const handleInit = async () => {
    setBusy(true)
    setErr('')
    setSuccess('')
    try {
      await InitConfig(baseDir.trim())
      setSuccess('Config initialized successfully.')
      loadStatus()
      onInitialized()
    } catch (e: unknown) {
      setErr(String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <h1 className="text-xl font-semibold text-white mb-1">Setup</h1>
      <p className="text-xs text-slate-400 mb-8">Initialize the aifs configuration file before creating instances.</p>

      {/* Current config status */}
      {status && (
        <div className={`mb-8 px-4 py-3 rounded-lg border text-xs space-y-1.5 ${
          status.exists
            ? 'bg-green-900/30 border-green-700/50 text-green-300'
            : 'bg-red-900/30 border-red-700/50 text-red-300'
        }`}>
          <p className="font-medium text-sm">{status.exists ? '✓ Config file found' : '✗ Config file not found'}</p>
          <p>Path: <span className="font-mono text-white">{status.path}</span></p>
          {status.exists && status.baseDir && (
            <p>Base dir: <span className="font-mono text-white">{status.baseDir}</span></p>
          )}
          {status.exists && !status.baseDir && (
            <p className="text-slate-400">Base dir: <span className="italic">default (~/.aifs/data)</span></p>
          )}
        </div>
      )}

      {/* Init form — only shown when config doesn't exist */}
      {status && !status.exists && (
        <div className="max-w-md space-y-6">
          <div>
            <label className="block text-sm text-slate-300 mb-1.5">
              Base directory <span className="text-slate-500 font-normal">(optional)</span>
            </label>
            <input
              type="text"
              value={baseDir}
              onChange={e => { setBaseDir(e.target.value); setErr('') }}
              placeholder="e.g. /data/aifs  or leave blank for default"
              disabled={busy}
              className="bg-slate-700 border border-slate-600 rounded px-3 py-2 text-sm w-full focus:outline-none focus:border-slate-400 transition-colors disabled:opacity-50"
            />
            <p className="text-slate-500 text-xs mt-1">
              All data (DB files, backups, WAL) will be stored under this directory.
              Leave blank to use the platform default (<span className="font-mono">~/.aifs/data</span>).
            </p>
          </div>

          <button
            disabled={busy}
            onClick={handleInit}
            className="px-6 py-2 text-sm rounded bg-blue-700 hover:bg-blue-600 disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex items-center gap-2"
          >
            {busy ? <><span className="animate-spin inline-block">↻</span> Initializing…</> : 'Initialize Config'}
          </button>

          {err && <p className="text-red-400 text-sm break-words">{err}</p>}
          {success && (
            <div className="flex items-start gap-2 bg-green-900/30 border border-green-700/50 rounded px-3 py-2 text-sm text-green-300">
              <span>✓</span><span>{success}</span>
            </div>
          )}

          <div className="bg-slate-800 border border-slate-700 rounded-lg p-4 text-xs text-slate-400 space-y-1.5">
            <p className="text-slate-300 font-medium">What happens next</p>
            <p>1. A default config file is created at the path shown above.</p>
            <p>2. Go to <span className="text-white">New Instance</span> to add your first PostgreSQL instance.</p>
            <p>3. Go to <span className="text-white">Instances</span> and click <span className="text-white">Start</span> to launch it.</p>
          </div>
        </div>
      )}

      {/* Already initialized */}
      {status?.exists && (
        <p className="text-sm text-slate-400 mb-4">The config file is already initialized.</p>
      )}

      {/* SSD tip — always visible */}
      <p className="text-amber-400/80 text-xs">
        💡 Strongly recommended: use a dedicated data disk (e.g. a separate SSD) as the aifs storage path for best performance and isolation.
      </p>
    </div>
  )
}
