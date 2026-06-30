import { useEffect, useState } from 'react'
import Instances from './pages/Instances'
import Snapshots from './pages/Snapshots'
import Restore from './pages/Restore'
import Bench from './pages/Bench'
import NewInstance from './pages/NewInstance'
import Setup from './pages/Setup'
import { GetConfigStatus } from './wailsjs/go'

type Page = 'instances' | 'new-instance' | 'snapshots' | 'restore' | 'bench' | 'setup'

const NAV: { id: Page; label: string }[] = [
  { id: 'setup', label: '⚙ Setup' },
  { id: 'instances', label: 'Instances' },
  { id: 'new-instance', label: '+ New Instance' },
  { id: 'snapshots', label: 'Snapshots' },
  { id: 'restore', label: 'Restore' },
  { id: 'bench', label: 'Bench' },
]

export default function App() {
  const [page, setPage] = useState<Page>('instances')

  // Auto-redirect to Setup if config file does not exist yet
  useEffect(() => {
    GetConfigStatus().then(s => {
      if (!s.exists) setPage('setup')
    }).catch(() => {/* ignore */})
  }, [])

  return (
    <div className="flex h-screen overflow-hidden bg-[#0f1117] text-slate-200">
      {/* Sidebar */}
      <nav className="w-44 flex-shrink-0 border-r border-slate-800 flex flex-col pt-6">
        <div className="px-4 mb-6 drag-region">
          <span className="text-lg font-bold text-white tracking-tight">aifs</span>
        </div>
        <ul className="space-y-1 px-2 no-drag">
          {NAV.map((n) => (
            <li key={n.id}>
              <button
                onClick={() => setPage(n.id)}
                className={`w-full text-left px-3 py-2 rounded-md text-sm transition-colors ${
                  page === n.id
                    ? 'bg-slate-700 text-white'
                    : n.id === 'new-instance'
                      ? 'text-blue-400 hover:bg-slate-800 hover:text-blue-300'
                      : 'text-slate-400 hover:bg-slate-800 hover:text-slate-200'
                }`}
              >
                {n.label}
              </button>
            </li>
          ))}
        </ul>
      </nav>

      {/* Main content */}
      <main className="flex-1 overflow-auto p-6">
        {page === 'instances' && <Instances />}
        {page === 'new-instance' && <NewInstance onCreated={() => setPage('instances')} onSetup={() => setPage('setup')} />}
        {page === 'snapshots' && <Snapshots />}
        {page === 'restore' && <Restore />}
        {page === 'bench' && <Bench />}
        {page === 'setup' && <Setup onInitialized={() => setPage('instances')} />}
      </main>
    </div>
  )
}
