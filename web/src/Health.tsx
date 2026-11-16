import { useEffect, useState } from 'react'
import { api, type Health, type HealthState } from './api'

const STATE: Record<HealthState, { label: string; icon: string }> = {
  ok: { label: 'Working', icon: '✓' },
  off: { label: 'Not in use', icon: '–' },
  warn: { label: 'Worth a look', icon: '!' },
  bad: { label: 'Not working', icon: '✕' },
}

/** Always an icon and words, never colour alone. */
export function StateBadge({ state }: { state: HealthState }) {
  return (
    <span className={`state ${state}`}>
      <i aria-hidden="true">{STATE[state].icon}</i>
      {STATE[state].label}
    </span>
  )
}

/** Answers "is it working?" without anyone reading logs on the host. */
export function HealthPage({ backHref }: { backHref: string }) {
  const [health, setHealth] = useState<Health | null>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let current = true
    const load = () => {
      if (document.hidden) return
      api
        .health()
        .then((h) => {
          if (!current) return
          setHealth(h)
          setFailed(false)
        })
        .catch(() => current && setFailed(true))
    }
    load()
    const id = setInterval(load, 5000)
    document.addEventListener('visibilitychange', load)
    return () => {
      current = false
      clearInterval(id)
      document.removeEventListener('visibilitychange', load)
    }
  }, [])

  const troubled = health?.checks.filter((c) => c.state === 'warn' || c.state === 'bad') ?? []

  return (
    <div className="settings health">
      <div className="heading">
        <a href={backHref}>← Back to the dashboard</a>
        <h1>Health</h1>
        <p>
          {health
            ? `Tattle ${health.version}, running for ${health.uptime}.`
            : failed
              ? 'Tattle is not answering. Is it still running on the host?'
              : 'Checking…'}
        </p>
      </div>

      {health && (
        <>
          <p className={`verdict ${health.state}`} role="status">
            {troubled.length === 0
              ? 'Everything is working.'
              : troubled.length === 1
                ? `One thing ${health.state === 'bad' ? 'is not working' : 'is worth a look'}: ${troubled[0].title.toLowerCase()}.`
                : `${troubled.length} things need attention.`}
          </p>

          {health.checks.map((c) => (
            <section key={c.id} className="card health-check">
              <div className="health-head">
                <h2>{c.title}</h2>
                <StateBadge state={c.state} />
              </div>
              <p>{c.summary}</p>
              {c.advice && <p className="advice">{c.advice}</p>}
              {c.facts.length > 0 && (
                <dl className="health-facts">
                  {c.facts.map((f) => (
                    <div key={f.label}>
                      <dt>{f.label}</dt>
                      <dd>{f.value}</dd>
                    </div>
                  ))}
                </dl>
              )}
            </section>
          ))}
        </>
      )}
    </div>
  )
}
