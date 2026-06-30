import { useState } from 'react'
import { BenchResult, RunBench } from '../wailsjs/go'

const ROWS: { key: keyof BenchResult; label: string; valueKey: keyof BenchResult; costKey: keyof BenchResult; unit: string; costUnit: string }[] = [
  {
    key: 'writeBigMiBs',
    label: 'Write big file',
    valueKey: 'writeBigMiBs',
    costKey: 'writeBigSecsPerFile',
    unit: 'MiB/s',
    costUnit: 's/file',
  },
  {
    key: 'readBigMiBs',
    label: 'Read big file',
    valueKey: 'readBigMiBs',
    costKey: 'readBigSecsPerFile',
    unit: 'MiB/s',
    costUnit: 's/file',
  },
  {
    key: 'writeSmallPerSec',
    label: 'Write small file',
    valueKey: 'writeSmallPerSec',
    costKey: 'writeSmallMsPerFile',
    unit: 'files/s',
    costUnit: 'ms/file',
  },
  {
    key: 'readSmallPerSec',
    label: 'Read small file',
    valueKey: 'readSmallPerSec',
    costKey: 'readSmallMsPerFile',
    unit: 'files/s',
    costUnit: 'ms/file',
  },
  {
    key: 'statPerSec',
    label: 'Stat file',
    valueKey: 'statPerSec',
    costKey: 'statMsPerFile',
    unit: 'files/s',
    costUnit: 'ms/file',
  },
]

export default function Bench() {
  const [path, setPath] = useState('')
  const [bigSize, setBigSize] = useState('10M')
  const [threads, setThreads] = useState('1')
  const [running, setRunning] = useState(false)
  const [result, setResult] = useState<BenchResult | null>(null)
  const [err, setErr] = useState('')

  const run = async () => {
    if (!path) return
    setRunning(true)
    setErr('')
    setResult(null)
    try {
      const res = await RunBench(path, bigSize, parseInt(threads, 10) || 1)
      setResult(res)
    } catch (e: unknown) {
      setErr(String(e))
    } finally {
      setRunning(false)
    }
  }

  return (
    <div>
      <h1 className="text-xl font-semibold text-white mb-6">Bench</h1>

      {/* Params */}
      <div className="flex flex-wrap gap-4 items-end mb-6">
        <div className="flex-1 min-w-48">
          <label className="block text-xs text-slate-400 mb-1">Mount path</label>
          <input
            type="text"
            value={path}
            onChange={(e) => setPath(e.target.value)}
            placeholder="/mnt/aifs  or  Z:\"
            className="w-full bg-slate-700 border border-slate-600 rounded px-3 py-1.5 text-sm focus:outline-none focus:border-slate-400"
          />
        </div>

        <div>
          <label className="block text-xs text-slate-400 mb-1">Big file size</label>
          <input
            type="text"
            value={bigSize}
            onChange={(e) => setBigSize(e.target.value)}
            placeholder="100M"
            className="w-28 bg-slate-700 border border-slate-600 rounded px-3 py-1.5 text-sm focus:outline-none focus:border-slate-400"
          />
        </div>

        <div>
          <label className="block text-xs text-slate-400 mb-1">Threads</label>
          <input
            type="number"
            min={1}
            max={32}
            value={threads}
            onChange={(e) => setThreads(e.target.value)}
            className="w-20 bg-slate-700 border border-slate-600 rounded px-3 py-1.5 text-sm focus:outline-none focus:border-slate-400"
          />
        </div>

        <button
          disabled={running || !path}
          onClick={run}
          className="px-6 py-1.5 text-sm rounded bg-blue-700 hover:bg-blue-600 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
        >
          {running ? 'Running…' : 'Run'}
        </button>
      </div>

      {err && <p className="text-red-400 text-sm mb-4 break-words">{err}</p>}

      {/* Results table */}
      {result && (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-xs text-slate-400 border-b border-slate-700">
                <th className="text-left py-2 pr-6">ITEM</th>
                <th className="text-right py-2 pr-6">VALUE</th>
                <th className="text-right py-2">COST</th>
              </tr>
            </thead>
            <tbody>
              {ROWS.map((row) => {
                const val = result[row.valueKey] as number
                const cost = result[row.costKey] as number
                return (
                  <tr key={row.key} className="border-b border-slate-800">
                    <td className="py-2 pr-6 text-slate-300">{row.label}</td>
                    <td className="py-2 pr-6 text-right font-mono text-white">
                      {val.toFixed(row.unit === 'MiB/s' ? 2 : 1)} {row.unit}
                    </td>
                    <td className="py-2 text-right font-mono text-slate-400">
                      {cost.toFixed(2)} {row.costUnit}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {!result && !running && !err && (
        <p className="text-slate-500 text-sm">
          Enter a mount path and click Run to start benchmarking.
        </p>
      )}
    </div>
  )
}
