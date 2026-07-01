import { useEffect, useState } from 'react'
import { AboutInfo, GetAboutInfo } from '../wailsjs/go'
import { BrowserOpenURL } from '../wailsjs/runtime'

const REPO_URL = 'https://github.com/mars-base/aifs'
const ISSUES_URL = 'https://github.com/mars-base/aifs/issues'
const CONTACT_EMAIL = 'aifs_issue@yeah.net'
const LICENSE = 'PolyForm Noncommercial License 1.0.0'

export default function About() {
  const [info, setInfo] = useState<AboutInfo | null>(null)
  const [emailCopied, setEmailCopied] = useState(false)

  useEffect(() => {
    GetAboutInfo().then(setInfo).catch(() => {/* ignore */})
  }, [])

  const copyEmail = async () => {
    await navigator.clipboard.writeText(CONTACT_EMAIL)
    setEmailCopied(true)
    setTimeout(() => setEmailCopied(false), 2000)
  }

  return (
    <div>
      <h1 className="text-xl font-semibold text-white mb-1">About aifs</h1>
      <p className="text-xs text-slate-400 mb-8">Version info and help resources.</p>

      <div className="max-w-md space-y-6">
        <div className="bg-slate-800 border border-slate-700 rounded-lg p-4 text-xs text-slate-400 space-y-1.5">
          <p className="text-slate-300 font-medium mb-1">Version</p>
          {info ? (
            <>
              <p>Version: <span className="font-mono text-white">{info.version}</span></p>
              <p>Build time: <span className="font-mono text-white">{info.buildTime}</span></p>
              <p>Platform: <span className="font-mono text-white">{info.os}/{info.arch}</span></p>
            </>
          ) : (
            <p className="italic">Loading…</p>
          )}
        </div>

        <div className="bg-slate-800 border border-slate-700 rounded-lg p-4 text-xs text-slate-400 space-y-1.5">
          <p className="text-slate-300 font-medium mb-1">Help</p>
          <p>
            Repository:{' '}
            <button
              onClick={() => BrowserOpenURL(REPO_URL)}
              className="font-mono text-blue-400 hover:text-blue-300 underline"
            >
              {REPO_URL}
            </button>
          </p>
          <p>License: <span className="text-white">{LICENSE}</span></p>
          <p className="text-slate-300 font-medium pt-1">Found a bug or need help?</p>
          <p>
            Please{' '}
            <button
              onClick={() => BrowserOpenURL(ISSUES_URL)}
              className="text-blue-400 hover:text-blue-300 underline"
            >
              submit an issue
            </button>
            .
          </p>
          <p>
            Or email{' '}
            <span className="font-mono text-white">{CONTACT_EMAIL}</span>
            {' '}
            <button
              onClick={copyEmail}
              className="text-blue-400 hover:text-blue-300 underline"
            >
              {emailCopied ? '✓ Copied' : 'Copy'}
            </button>
          </p>
        </div>
      </div>
    </div>
  )
}
