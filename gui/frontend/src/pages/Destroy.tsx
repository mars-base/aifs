import { useEffect, useState } from 'react'
import {
  DestroyInstance,
  InstanceInfo,
  ListInstances,
  ShowAlert,
  ShowConfirm,
} from '../wailsjs/go'

export default function Destroy() {
  const [instances, setInstances] = useState<InstanceInfo[]>([])
  const [destroyTarget, setDestroyTarget] = useState('')
  const [cleanData, setCleanData] = useState(false)
  const [destroyBusy, setDestroyBusy] = useState(false)
  const [destroyErr, setDestroyErr] = useState('')
  const [destroySuccess, setDestroySuccess] = useState('')

  const refreshInstances = () => {
    ListInstances().then(list => {
      const sorted = (list ?? []).slice().sort((a, b) => a.name.localeCompare(b.name))
      setInstances(sorted)
      setDestroyTarget(prev => (sorted.some(i => i.name === prev) ? prev : sorted[0]?.name ?? ''))
    }).catch(() => setInstances([]))
  }

  useEffect(() => {
    refreshInstances()
  }, [])

  const target = instances.find(i => i.name === destroyTarget)
  const blockReasons = []
  if (target?.running) blockReasons.push('is running')
  if (target?.mountPath) blockReasons.push(`is mounted at ${target.mountPath}`)
  const blocked = blockReasons.length > 0

  const handleDestroy = async () => {
    if (!destroyTarget) return

    // Pre-check: refuse to destroy a running or mounted instance — the user
    // must stop/umount it from the Instances page first, since destroying
    // out from under a live container/mount would leave it in a broken state.
    if (target?.running || target?.mountPath) {
      const reasons = []
      if (target.running) reasons.push('is still running')
      if (target.mountPath) reasons.push(`is mounted at ${target.mountPath}`)
      await ShowAlert(
        'Cannot Destroy Instance',
        `Instance "${destroyTarget}" ${reasons.join(' and ')}.\n\nGo to Instances and Umount / Stop it first, then retry.`
      )
      return
    }

    const warning = cleanData
      ? `This will stop and remove the container for "${destroyTarget}", AND permanently delete its host data, WAL and backup stanza.\n\nThis cannot be undone.`
      : `This will stop and remove the container for "${destroyTarget}" and remove it from the config.\n\nHost data directories will be preserved.`
    const ok = await ShowConfirm('Destroy Instance', warning)
    if (!ok) return

    setDestroyBusy(true)
    setDestroyErr('')
    setDestroySuccess('')
    try {
      await DestroyInstance(destroyTarget, cleanData)
      setDestroySuccess(`Instance "${destroyTarget}" destroyed.`)
      setCleanData(false)
      refreshInstances()
    } catch (e: unknown) {
      setDestroyErr(String(e))
    } finally {
      setDestroyBusy(false)
    }
  }

  return (
    <div>
      <h1 className="text-xl font-semibold text-white mb-1">Destroy Instance</h1>
      <p className="text-xs font-bold text-yellow-400 mb-8">
        This will destroy the project instance and all backed up data, proceed with caution!
      </p>

      {instances.length === 0 ? (
        <p className="text-sm text-slate-500">No instances configured.</p>
      ) : (
        <div className="max-w-md border border-red-900/60 rounded-lg p-4">
          <p className="text-sm font-semibold text-red-400 mb-1">Danger zone</p>
          <p className="text-xs text-slate-500 mb-4">
            This is irreversible. The instance must be stopped and unmounted first.
          </p>

          <div className="space-y-3">
            <div>
              <label className="block text-xs text-slate-400 mb-1">Instance</label>
              <select
                value={destroyTarget}
                onChange={e => { setDestroyTarget(e.target.value); setDestroyErr(''); setDestroySuccess('') }}
                disabled={destroyBusy}
                className="bg-slate-700 border border-slate-600 rounded px-3 py-2 text-sm w-full focus:outline-none focus:border-slate-400 disabled:opacity-50"
              >
                {instances.map(i => (
                  <option key={i.name} value={i.name}>
                    {i.name}{i.running ? ' (running)' : ''}
                  </option>
                ))}
              </select>
            </div>

            {blocked && (
              <p className="text-xs text-yellow-400 bg-yellow-900/20 border border-yellow-800/50 rounded px-3 py-2">
                ⚠ "{destroyTarget}" {blockReasons.join(' and ')} — go to{' '}
                <span className="text-yellow-200">Instances</span> and Umount / Stop it before destroying.
              </p>
            )}

            <label className="flex items-start gap-2 cursor-pointer select-none">
              <input
                type="checkbox"
                checked={cleanData}
                onChange={e => setCleanData(e.target.checked)}
                disabled={destroyBusy}
                className="mt-0.5"
              />
              <span className="text-xs text-slate-400">
                Also delete host data, WAL and backup stanza{' '}
                <span className="text-red-400">(irreversible)</span>
              </span>
            </label>

            <button
              disabled={destroyBusy || !destroyTarget || blocked}
              onClick={handleDestroy}
              className="w-full px-4 py-2 text-sm rounded bg-red-900/60 hover:bg-red-800 border border-red-800 disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex items-center justify-center gap-2"
            >
              {destroyBusy ? <><span className="animate-spin inline-block">↻</span> Destroying…</> : `Destroy "${destroyTarget}"`}
            </button>

            {destroyErr && <p className="text-red-400 text-xs break-words">{destroyErr}</p>}
            {destroySuccess && (
              <div className="flex items-start gap-2 bg-green-900/30 border border-green-700/50 rounded px-3 py-2 text-xs text-green-300">
                <span>✓</span>
                <span>{destroySuccess}</span>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
