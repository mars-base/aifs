import { useEffect, useState } from 'react'
import Instances from './pages/Instances'
import Snapshots from './pages/Snapshots'
import Restore from './pages/Restore'
import Bench from './pages/Bench'
import NewInstance from './pages/NewInstance'
import Destroy from './pages/Destroy'
import Setup from './pages/Setup'
import About from './pages/About'
import { GetConfigStatus, GetUpdateInfo } from './wailsjs/go'
import type { UpdateInfo } from './wailsjs/go'

type Page = 'instances' | 'new-instance' | 'destroy' | 'snapshots' | 'restore' | 'bench' | 'setup' | 'about'

const NAV: { id: Page; label: string }[] = [
  { id: 'setup', label: '⚙ Setup' },
  { id: 'instances', label: 'Instances' },
  { id: 'new-instance', label: '+ New Instance' },
  { id: 'snapshots', label: 'Snapshots' },
  { id: 'restore', label: 'Restore' },
  { id: 'bench', label: 'Bench' },
  { id: 'destroy', label: 'Destroy' },
]

export default function App() {
  const [page, setPage] = useState<Page>('instances')
  const [configReady, setConfigReady] = useState(false)
  const [updateInfo, setUpdateInfo] = useState<UpdateInfo | null>(null)

  // Auto-redirect to Setup if config file does not exist yet
  useEffect(() => {
    GetConfigStatus().then(s => {
      setConfigReady(s.exists)
      if (!s.exists) setPage('setup')
    }).catch(() => {/* ignore */})

    // Poll for update info — the backend checkForUpdate() goroutine may take
    // a few seconds (GitHub API call), so retry every second until we get a
    // result or give up after ~12s.
    let attempts = 0
    const poll = setInterval(() => {
      GetUpdateInfo().then(info => {
        if (info) { setUpdateInfo(info); clearInterval(poll) }
        else if (++attempts >= 12) clearInterval(poll)
      }).catch(() => { clearInterval(poll) })
    }, 1000)
    return () => clearInterval(poll)
  }, [])

  const goPage = (id: Page) => {
    // Block navigation to non-Setup pages when config is missing.
    // About is exempt — version/help info should always be reachable.
    if (!configReady && id !== 'setup' && id !== 'about') return
    setPage(id)
  }

  const onSetupDone = () => {
    setConfigReady(true)
    setPage('instances')
  }

  return (
    <div className="flex h-screen overflow-hidden bg-[#0f1117] text-slate-200">
      {/* Sidebar */}
      <nav className="w-44 flex-shrink-0 border-r border-slate-800 flex flex-col pt-6">
        <div className="px-4 mb-6 drag-region">
          <span className="text-lg font-bold text-white tracking-tight">aifs</span>
        </div>
        <ul className="space-y-1 px-2 no-drag">
          {NAV.map((n) => {
            const disabled = !configReady && n.id !== 'setup'
            return (
              <li key={n.id}>
                <button
                  disabled={disabled}
                  onClick={() => goPage(n.id)}
                  className={`w-full text-left px-3 py-2 rounded-md text-sm transition-colors ${
                    disabled
                      ? 'text-slate-600 cursor-not-allowed'
                      : page === n.id
                        ? 'bg-slate-700 text-white'
                        : n.id === 'new-instance'
                          ? 'text-blue-400 hover:bg-slate-800 hover:text-blue-300'
                          : n.id === 'destroy'
                            ? 'text-red-400 hover:bg-slate-800 hover:text-red-300'
                            : 'text-slate-400 hover:bg-slate-800 hover:text-slate-200'
                  }`}
                >
                  {n.label}
                </button>
              </li>
            )
          })}
        </ul>

        <div className="mt-auto px-2 pb-4 pt-2 border-t border-slate-800 no-drag">
          <button
            onClick={() => goPage('about')}
            className="w-full text-left px-3 py-2 rounded-md text-sm transition-colors bg-[#0f1117] text-slate-500 hover:text-slate-300"
          >
            ⓘ About / Help
          </button>
          {updateInfo?.updateAvailable && (
            <p className="px-3 pt-1 text-xs text-amber-400/80">
              ⬆ {updateInfo.latestVersion} available
            </p>
          )}
        </div>
      </nav>

      {/* Main content */}
      <main className="flex-1 overflow-auto p-6">
        {page === 'instances' && <Instances onNewInstance={() => setPage('new-instance')} />}
        {page === 'new-instance' && <NewInstance onCreated={() => setPage('instances')} onSetup={() => setPage('setup')} />}
        {page === 'destroy' && <Destroy />}
        {page === 'snapshots' && <Snapshots />}
        {page === 'restore' && <Restore />}
        {page === 'bench' && <Bench />}
        {page === 'setup' && <Setup onInitialized={onSetupDone} />}
        {page === 'about' && <About />}
      </main>
    </div>
  )
}
