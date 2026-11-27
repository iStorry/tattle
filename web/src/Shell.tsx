import type { HealthState, Status } from './api'
import { Icon, type IconName } from './icons'
import type { Theme } from './theme'
import { formatHash, RANGES, type View } from './url'

const DAY = 24 * 60 * 60
const WEEK = 7 * DAY

const PAGES: { page: View['page']; label: string; icon: IconName }[] = [
  { page: 'dashboard', label: 'Dashboard', icon: 'dashboard' },
  { page: 'map', label: 'Topology', icon: 'topology' },
  { page: 'machines', label: 'Machines', icon: 'host' },
  { page: 'health', label: 'Health', icon: 'health' },
]

/** The column of icons down the left that moves between pages. */
export function Rail(p: {
  view: View
  health: HealthState | undefined
  theme: Theme
  onTheme: () => void
  canSignOut: boolean
  onSignOut: () => void
}) {
  // Moving between pages keeps the time range, and the machine being looked
  // at, but leaves a destination's detail behind.
  const href = (page: View['page']) => formatHash({ ...p.view, page, peer: '', node: '' })
  const troubled = p.health === 'warn' || p.health === 'bad'
  return (
    <nav className="rail" aria-label="Pages">
      <a className="rail-logo" href={href('dashboard')} aria-label="Tattle dashboard">
        <svg viewBox="0 0 32 32" aria-hidden="true">
          <rect width="32" height="32" rx="9" />
          <path d="M9 11h14M16 11v12" />
          <circle cx="23.5" cy="22" r="2.4" />
        </svg>
      </a>
      {PAGES.map((x) => (
        <a key={x.page} className="rail-item" href={href(x.page)} aria-current={p.view.page === x.page ? 'page' : undefined}>
          <Icon name={x.icon} />
          <span className="rail-label">{x.label}</span>
          {x.page === 'health' && troubled && (
            <i className="rail-dot" data-state={p.health}>
              <span className="sr-only">{p.health === 'bad' ? ': not working' : ': worth a look'}</span>
            </i>
          )}
        </a>
      ))}
      <div className="rail-end">
        <a
          className="rail-item"
          href={href('settings')}
          aria-current={p.view.page === 'settings' ? 'page' : undefined}
        >
          <Icon name="settings" />
          <span className="rail-label">Settings</span>
        </a>
        <button type="button" className="rail-item" onClick={p.onTheme}>
          <Icon name={p.theme === 'dark' ? 'sun' : 'moon'} />
          <span className="rail-label">{p.theme === 'dark' ? 'Light look' : 'Dark look'}</span>
        </button>
        {p.canSignOut && (
          <button type="button" className="rail-item" onClick={p.onSignOut}>
            <Icon name="signout" />
            <span className="rail-label">Sign out</span>
          </button>
        )}
      </div>
    </nav>
  )
}

/** The bar across the top: where you are, the time range, and whether figures are live. */
export function TopBar(p: {
  title: string
  status: Status
  failed: boolean
  range?: number
  onRange?: (seconds: number) => void
}) {
  return (
    <header className="topbar">
      <div className="topbar-title">
        <strong>{p.title}</strong>
        {(p.status.interfaces.length > 0 || !p.status.demo) && (
          <span className="site" title="The network interfaces Tattle listens on">
            <Icon name="host" />
            {p.status.interfaces.length > 0 ? `Watching ${p.status.interfaces.join(', ')}` : 'Not listening'}
          </span>
        )}
        {p.status.demo && <span className="pill warn">Demo data</span>}
      </div>
      {p.range !== undefined && p.onRange && (
        <div className="segmented" role="group" aria-label="Time range">
          {RANGES.filter(
            // Longer ranges are offered once that much history is kept.
            (r) =>
              r.seconds <= WEEK ||
              r.seconds === p.range ||
              p.status.retentionDays * DAY > (r.seconds === 30 * DAY ? WEEK : 30 * DAY),
          ).map((r) => (
            <button key={r.seconds} aria-pressed={p.range === r.seconds} onClick={() => p.onRange?.(r.seconds)}>
              {r.label}
            </button>
          ))}
        </div>
      )}
      <span className="live" data-lost={p.failed || undefined} role="status">
        <i aria-hidden="true" />
        {p.failed ? 'Reconnecting…' : 'Live'}
      </span>
    </header>
  )
}
