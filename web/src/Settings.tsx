import { useState, type FormEvent } from 'react'
import { api, type Scope, type Settings, type SettingsInfo } from './api'
import { PasswordCard } from './Auth'

const SHOW: { value: Scope; title: string; hint: string }[] = [
  { value: 'all', title: 'Show everything', hint: 'Traffic inside your network and traffic to and from the internet.' },
  {
    value: 'internet',
    title: 'Hide traffic inside your network',
    hint: 'Only show what went out to, or came in from, the internet.',
  },
  {
    value: 'local',
    title: 'Hide internet traffic',
    hint: 'Only show what your machines and devices said to each other.',
  },
]

export function SettingsPage(p: { info: SettingsInfo; canChangePassword: boolean; onSaved: (info: SettingsInfo) => void }) {
  const [draft, setDraft] = useState<Settings>(p.info.settings)
  const [entry, setEntry] = useState('')
  const [state, setState] = useState<'idle' | 'saving' | 'saved'>('idle')
  const [error, setError] = useState('')

  const change = (patch: Partial<Settings>) => {
    setDraft({ ...draft, ...patch })
    setState('idle')
    setError('')
  }

  // Anything typed in the box but not yet added is saved too, so pressing
  // Save without pressing Add first does not lose it.
  const pending = entry.trim()
  const networks = pending && !draft.localNets.includes(pending) ? [...draft.localNets, pending] : draft.localNets

  const add = () => {
    if (!pending) return
    change({ localNets: networks })
    setEntry('')
  }

  const save = async (e: FormEvent) => {
    e.preventDefault()
    setState('saving')
    setError('')
    try {
      const info = await api.saveSettings({ ...draft, localNets: networks })
      setDraft(info.settings)
      setEntry('')
      setState('saved')
      p.onSaved(info)
    } catch (err) {
      setState('idle')
      setError(err instanceof Error ? err.message : 'The settings could not be saved.')
    }
  }

  return (
    <div className="settings">
      <div className="heading">
        <a href="#/">← Back to the dashboard</a>
        <h1>Settings</h1>
        <p>These apply to everyone who opens this dashboard.</p>
      </div>
      <form className="settings-form" onSubmit={save}>

      <section className="card">
        <h2>What counts as your network</h2>
        <p className="help">
          Tattle labels every connection as either inside your network or internet. These rules decide which. Changing
          them re-labels past traffic too.
        </p>

        <div className="field">
          <span className="field-label">Found on this host</span>
          {p.info.detected.length > 0 ? (
            <ul className="chips">
              {p.info.detected.map((n) => (
                <li key={n}>{n}</li>
              ))}
            </ul>
          ) : (
            <p className="help">None found.</p>
          )}
          <p className="help">
            Read from the host's own addresses. If the host is 10.20.30.5, everything starting 10.20.30 is yours.
          </p>
        </div>

        <div className="field">
          <label className="field-label" htmlFor="net">
            Your own additions
          </label>
          {draft.localNets.length > 0 && (
            <ul className="chips">
              {draft.localNets.map((n) => (
                <li key={n}>
                  {n}
                  <button
                    type="button"
                    aria-label={`Remove ${n}`}
                    onClick={() => change({ localNets: draft.localNets.filter((x) => x !== n) })}
                  >
                    ×
                  </button>
                </li>
              ))}
            </ul>
          )}
          <div className="add">
            <input
              id="net"
              placeholder="10.20.30.0/24"
              value={entry}
              onChange={(e) => {
                setEntry(e.target.value)
                setState('idle')
                setError('')
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  add()
                }
              }}
            />
            <button type="button" className="button" onClick={add} disabled={!pending}>
              Add
            </button>
          </div>
          <p className="help">
            A network such as 10.20.30.0/24 (every address starting 10.20.30), or a single address. Useful for another
            VLAN or a VPN.
          </p>
        </div>

        <label className="check">
          <input
            type="checkbox"
            checked={draft.privateIsLocal}
            onChange={(e) => change({ privateIsLocal: e.target.checked })}
          />
          <span>
            Count every private address as your network
            <small>
              Addresses starting 192.168, 10, or 172.16 to 172.31 cannot be reached from the internet, so they are
              almost always yours. Turn this off to count only the networks listed above.
            </small>
          </span>
        </label>
      </section>

      <section className="card">
        <h2>What the dashboard shows when it opens</h2>
        <div className="radios">
          {SHOW.map((o) => (
            <label key={o.value} className="check">
              <input type="radio" name="show" checked={draft.show === o.value} onChange={() => change({ show: o.value })} />
              <span>
                {o.title}
                <small>{o.hint}</small>
              </span>
            </label>
          ))}
        </div>
        <p className="help">You can still switch on the dashboard at any time; this sets the starting point.</p>
      </section>

      <section className="card">
        <h2>How long history is kept</h2>
        <label className="field">
          <span className="field-label">Days of history</span>
          <input
            className="input days"
            type="number"
            min={1}
            max={90}
            step={1}
            required
            value={Number.isFinite(draft.retentionDays) ? draft.retentionDays : ''}
            onChange={(e) => change({ retentionDays: e.target.valueAsNumber })}
          />
          <span className="help">
            Between 1 and 90. Older traffic is deleted from the host. Keeping more uses more disk space, and choosing
            fewer days deletes the older history as soon as you save.
          </span>
        </label>
      </section>

      <div className="actions">
        <button className="button primary" disabled={state === 'saving'}>
          {state === 'saving' ? 'Saving…' : 'Save settings'}
        </button>
        {state === 'saved' && <span role="status">Saved.</span>}
        {error && (
          <span role="alert" className="error">
            {error}
          </span>
        )}
      </div>
      </form>
      {p.canChangePassword && <PasswordCard />}
    </div>
  )
}
