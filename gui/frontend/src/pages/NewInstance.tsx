import { useEffect, useState } from 'react'
import { CreateInstance, GetConfigStatus } from '../wailsjs/go'

interface Props {
  onCreated: () => void
  onSetup?: () => void
}

export default function NewInstance({ onCreated, onSetup }: Props) {
  const [name, setName] = useState('')
  const [dataDir, setDataDir] = useState('')
  const [pitr, setPitr] = useState(true)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [success, setSuccess] = useState('')
  const [configExists, setConfigExists] = useState<boolean | null>(null)

  useEffect(() => {
    GetConfigStatus().then(s => setConfigExists(s.exists)).catch(() => setConfigExists(false))
  }, [])

  const nameErr = (() => {
    if (!name) return ''
    if (!/^[a-zA-Z0-9_-]+$/.test(name)) return 'Only letters, digits, - and _ are allowed'
    return ''
  })()

  const handleCreate = async () => {
    setBusy(true)
    setErr('')
    setSuccess('')
    try {
      await CreateInstance({ name: name.trim(), data_dir: dataDir.trim(), pitr_enabled: pitr })
      setSuccess(`Instance "${name}" created. Go to Instances to start it.`)
      setName('')
      setDataDir('')
      setPitr(true)
      onCreated()
    } catch (e: unknown) {
      setErr(String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <h1 className="text-xl font-semibold text-white mb-1">New Instance</h1>
      <p className="text-xs text-slate-400 mb-8">
        Add a new PostgreSQL instance. Port numbers are assigned automatically.
        A random password will be generated.
      </p>

      <div className="max-w-md space-y-6">

        {/* Config not initialized warning */}
        {configExists === false && (
          <div className="flex items-start gap-3 px-4 py-3 bg-red-900/30 border border-red-700/50 rounded-lg text-sm text-red-300">
            <span className="mt-0.5">✗</span>
            <div>
              <p className="font-medium">Config not initialized</p>
              <p className="text-xs text-red-400/80 mt-0.5">
                Please go to{' '}
                {onSetup
                  ? <button onClick={onSetup} className="underline hover:text-red-200 transition-colors">Setup</button>
                  : <span className="text-red-200">Setup</span>
                }{' '}
                and initialize the config file first.
              </p>
            </div>
          </div>
        )}
        <div>
          <label className="block text-sm text-slate-300 mb-1.5">
            Instance name <span className="text-red-400">*</span>
          </label>
          <input
            type="text"
            value={name}
            onChange={e => { setName(e.target.value); setErr(''); setSuccess('') }}
            placeholder="e.g. ai03"
            disabled={busy}
            className={`bg-slate-700 border rounded px-3 py-2 text-sm w-full focus:outline-none transition-colors disabled:opacity-50 ${
              nameErr ? 'border-red-500 focus:border-red-400' : 'border-slate-600 focus:border-slate-400'
            }`}
          />
          {nameErr
            ? <p className="text-red-400 text-xs mt-1">{nameErr}</p>
            : <p className="text-slate-500 text-xs mt-1">Letters, digits, <code>-</code> and <code>_</code> only</p>
          }
        </div>

        {/* Data directory */}
        <div>
          <label className="block text-sm text-slate-300 mb-1.5">Data directory <span className="text-slate-500 font-normal">(optional)</span></label>
          <input
            type="text"
            value={dataDir}
            onChange={e => setDataDir(e.target.value)}
            placeholder="Leave blank to use default"
            disabled={busy}
            className="bg-slate-700 border border-slate-600 rounded px-3 py-2 text-sm w-full focus:outline-none focus:border-slate-400 transition-colors disabled:opacity-50"
          />
        </div>

        {/* PITR */}
        <div>
          <label className="flex items-center gap-3 cursor-pointer select-none">
            <div
              onClick={() => !busy && setPitr(v => !v)}
              className={`w-10 h-5 rounded-full transition-colors relative ${pitr ? 'bg-blue-600' : 'bg-slate-600'} ${busy ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer'}`}
            >
              <span className={`absolute top-0.5 w-4 h-4 rounded-full bg-white shadow transition-transform ${pitr ? 'translate-x-5' : 'translate-x-0.5'}`} />
            </div>
            <div>
              <span className="text-sm text-slate-300">Enable PITR (pgBackRest)</span>
              <p className="text-xs text-slate-500 mt-0.5">Continuous WAL archiving and point-in-time recovery</p>
            </div>
          </label>
        </div>

        {/* Submit */}
        <div className="pt-2">
          <button
            disabled={busy || !name || !!nameErr || !configExists}
            onClick={handleCreate}
            className="px-6 py-2 text-sm rounded bg-blue-700 hover:bg-blue-600 disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex items-center gap-2"
          >
            {busy ? <><span className="animate-spin inline-block">↻</span> Creating…</> : 'Create Instance'}
          </button>
        </div>

        {err && <p className="text-red-400 text-sm break-words">{err}</p>}
        {success && (
          <div className="flex items-start gap-2 bg-green-900/30 border border-green-700/50 rounded px-3 py-2 text-sm text-green-300">
            <span>✓</span>
            <span>{success}</span>
          </div>
        )}
      </div>

      {/* Info card */}
      <div className="mt-10 max-w-md bg-slate-800 border border-slate-700 rounded-lg p-4 text-xs text-slate-400 space-y-1.5">
        <p className="text-slate-300 font-medium">What happens next</p>
        <p>1. Instance is added to the config file — no containers are started yet.</p>
        <p>2. Go to <span className="text-white">Instances</span> and click <span className="text-white">Start</span> to initialise and launch the PostgreSQL container.</p>
        <p>3. After first start, go to <span className="text-white">Snapshots</span> and create a <span className="text-white">full backup</span> to enable PITR.</p>
      </div>
    </div>
  )
}
