import { useEffect, useMemo, useState } from 'react'
import {
  api,
  type Conversation,
  type Machine,
  type Peer,
  type Scope,
  type SettingsInfo,
  type Share,
  type Split,
  type Status,
  type Timeline,
} from './api'
import { Spark, TrafficChart } from './Chart'
import { ago, bytes, count, KIND_LABEL, plural, service } from './format'
import { SettingsPage } from './Settings'

const RANGES = [
  { label: '15 min', phrase: '15 minutes', seconds: 15 * 60 },
  { label: '1 hour', phrase: 'hour', seconds: 60 * 60 },
  { label: '24 hours', phrase: '24 hours', seconds: 24 * 60 * 60 },
  { label: '7 days', phrase: '7 days', seconds: 7 * 24 * 60 * 60 },
]

type Tab = 'out' | 'in' | 'log'
type Sort = 'recent' | 'data'

const SCOPES: { value: Scope; label: string }[] = [
  { value: 'all', label: 'Show everything' },
  { value: 'internet', label: 'Hide inside your network' },
  { value: 'local', label: 'Hide internet' },
]

/** The page named by the address bar: "#/settings" or the dashboard. */
function useRoute(): string {
  const [hash, setHash] = useState(window.location.hash)
  useEffect(() => {
    const on = () => setHash(window.location.hash)
    window.addEventListener('hashchange', on)
    return () => window.removeEventListener('hashchange', on)
  }, [])
  return hash === '#/settings' ? 'settings' : 'dashboard'
}

function ScopeTag({ local, incoming }: { local: boolean; incoming?: boolean }) {
  if (local) return <span className="tag local">Inside your network</span>
  return <span className={incoming ? 'tag warn' : 'tag internet'}>{incoming ? 'From the internet' : 'Internet'}</span>
}

type Data = {
  machines: Machine[]
  timeline: Timeline
  peers: Peer[]
  log: Conversation[]
  split: Split
}

/** Remembers a search box's value only once the typing has paused. */
function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms)
    return () => clearTimeout(id)
  }, [value, ms])
  return v
}

export default function App() {
  const [range, setRange] = useState(RANGES[1].seconds)
  const [machine, setMachine] = useState('')
  const [tab, setTab] = useState<Tab>('out')
  const [sort, setSort] = useState<Sort>('recent')
  // Unknown until the saved settings say what the dashboard should open with.
  const [scope, setScope] = useState<Scope | null>(null)
  const [info, setInfo] = useState<SettingsInfo | null>(null)
  const route = useRoute()
  const [search, setSearch] = useState('')
  const query = useDebounced(search.trim(), 250)

  const [status, setStatus] = useState<Status | null>(null)
  const [data, setData] = useState<Data | null>(null)
  const [failed, setFailed] = useState(false)
  // The host's clock is the reference for "3 min ago", not the browser's.
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    let current = true
    api
      .settings()
      .then((i) => {
        if (!current) return
        setInfo(i)
        setScope(i.settings.show)
      })
      .catch(() => current && setFailed(true))
    return () => {
      current = false
    }
  }, [])

  useEffect(() => {
    if (scope === null) return
    let current = true
    const load = async () => {
      if (document.hidden) return
      try {
        const [st, machines, timeline, peers, log, split] = await Promise.all([
          api.status(),
          api.machines(range, scope),
          api.timeline(range, machine, scope),
          api.peers(range, machine, scope),
          api.activity(range, machine, scope, query),
          api.split(range, machine),
        ])
        if (!current) return
        setStatus(st)
        setNow(st.now)
        setData({ machines, timeline, peers, log, split })
        setFailed(false)
      } catch {
        if (current) setFailed(true)
      }
    }
    load()
    const id = setInterval(load, 3000)
    document.addEventListener('visibilitychange', load)
    return () => {
      current = false
      clearInterval(id)
      document.removeEventListener('visibilitychange', load)
    }
  }, [range, machine, query, scope, info])

  const names = useMemo(() => new Map(data?.machines.map((m) => [m.id, m]) ?? []), [data])
  const selected = machine ? names.get(machine) : undefined

  if (!data || !status || !info || scope === null) {
    return (
      <div className="splash">
        <Logo />
        <p>{failed ? 'Tattle is not answering. Is it still running on the host?' : 'Loading…'}</p>
      </div>
    )
  }

  const matches = (p: Peer) => {
    if (!query) return true
    const q = query.toLowerCase()
    return (
      p.name.toLowerCase().includes(q) ||
      p.ip.includes(q) ||
      (names.get(p.peerId)?.name.toLowerCase().includes(q) ?? false)
    )
  }
  const order = (a: Peer, b: Peer) =>
    sort === 'data' ? b.sent + b.received - (a.sent + a.received) : b.lastSeen - a.lastSeen
  const outgoing = data.peers.filter((p) => p.direction === 'out')
  const incoming = data.peers.filter((p) => p.direction === 'in')
  const shown = (tab === 'out' ? outgoing : incoming).filter(matches).sort(order)

  const sent = data.timeline.points.reduce((a, p) => a + p.sent, 0)
  const received = data.timeline.points.reduce((a, p) => a + p.received, 0)
  const activeMachines = data.machines.filter((m) => m.lastSeen > 0).length
  const rangeLabel = RANGES.find((r) => r.seconds === range)?.phrase ?? ''

  return (
    <div className="app">
      <header className="top">
        <Logo />
        <span className="watching">
          {status.demo ? 'Demo data' : `Watching ${status.interfaces.join(', ')}`}
          {failed && ' · connection lost, retrying'}
        </span>
        {route === 'dashboard' && (
          <div className="segmented" role="group" aria-label="Time range">
            {RANGES.map((r) => (
              <button key={r.seconds} aria-pressed={range === r.seconds} onClick={() => setRange(r.seconds)}>
                {r.label}
              </button>
            ))}
          </div>
        )}
        <a className="nav-link" href={route === 'settings' ? '#/' : '#/settings'}>
          {route === 'settings' ? 'Dashboard' : 'Settings'}
        </a>
      </header>

      {status.demo && (
        <p className="banner">
          This is invented traffic from a pretend home lab. Run Tattle on your Proxmox host without{' '}
          <code>--demo</code> to see your own machines.
        </p>
      )}

      {route === 'settings' ? (
        <SettingsPage
          info={info}
          onSaved={(saved) => {
            setInfo(saved)
            setScope(saved.settings.show)
          }}
        />
      ) : (
      <div className="layout">
        <nav className="machines" aria-label="Machines">
          <h2>Machines</h2>
          <button className="machine" aria-current={machine === ''} onClick={() => setMachine('')}>
            <span className="machine-name">All machines</span>
            <span className="machine-meta">
              {activeMachines} of {data.machines.length} active
            </span>
          </button>
          {data.machines.map((m) => (
            <button key={m.id} className="machine" aria-current={machine === m.id} onClick={() => setMachine(m.id)}>
              <span className="machine-name">
                {m.name}
                <span className="kind">{KIND_LABEL[m.kind]}</span>
              </span>
              <span className="machine-meta">
                {m.lastSeen ? `${bytes(m.sent + m.received)} · ${ago(m.lastSeen, now)}` : 'No traffic'}
              </span>
              <Spark values={m.spark} />
            </button>
          ))}
        </nav>

        <main>
          <div className="heading">
            <h1>{selected ? selected.name : 'All machines'}</h1>
            <p>
              {selected
                ? [
                    selected.kind === 'host' ? 'The Proxmox host itself' : `${KIND_LABEL[selected.kind]} ${selected.vmid}`,
                    ...selected.ips.slice(0, 2),
                    selected.present ? '' : 'no longer exists',
                  ]
                    .filter(Boolean)
                    .join(' · ')
                : `Everything your VMs, containers and the host did on the network in the last ${rangeLabel}.`}
            </p>
            <div className="scope" role="group" aria-label="What to show">
              {SCOPES.map((s) => (
                <button key={s.value} aria-pressed={scope === s.value} onClick={() => setScope(s.value)}>
                  {s.label}
                </button>
              ))}
            </div>
            {scope !== 'all' && (
              <p className="scope-note">
                {scope === 'internet'
                  ? 'Traffic that stayed inside your network is hidden from the figures, chart and lists below.'
                  : 'Traffic to and from the internet is hidden from the figures, chart and lists below.'}
              </p>
            )}
          </div>

          <section className="tiles">
            <Tile label="Places contacted" value={count(selected ? selected.destinations : outgoing.length)} />
            <Tile label="Callers" value={count(incoming.length)} hint="Others that connected in" />
            <Tile label="Sent" value={bytes(sent)} />
            <Tile label="Received" value={bytes(received)} />
          </section>

          <section className="card">
            <h2>Where the traffic went</h2>
            <SplitBar split={data.split} info={info} />
          </section>

          <section className="card">
            <h2>Traffic over time</h2>
            <TrafficChart data={data.timeline} range={range} />
          </section>

          <section className="card">
            <div className="toolbar">
              <div className="tabs" role="tablist">
                <TabButton id="out" tab={tab} set={setTab} label="Reaching out" n={outgoing.length} />
                <TabButton id="in" tab={tab} set={setTab} label="Coming in" n={incoming.length} />
                <TabButton id="log" tab={tab} set={setTab} label="Detailed log" />
              </div>
              <input
                type="search"
                placeholder="Search a name or address"
                aria-label="Search"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
              {tab !== 'log' && (
                <select aria-label="Sort" value={sort} onChange={(e) => setSort(e.target.value as Sort)}>
                  <option value="recent">Most recent first</option>
                  <option value="data">Most data first</option>
                </select>
              )}
            </div>

            <p className="explain">
              {tab === 'out' && 'Websites, services and devices that your machines started a conversation with.'}
              {tab === 'in' && 'Devices and outside addresses that started a conversation with your machines.'}
              {tab === 'log' && 'Every kind of connection, one line each, newest first.'}
            </p>

            {tab === 'log' ? (
              <LogList rows={data.log} names={names} now={now} />
            ) : (
              <PeerList peers={shown} names={names} now={now} showMachines={!selected} />
            )}
          </section>

          <footer>
            Tattle {status.version} · keeps {status.retentionDays} days of history
            {status.dropped > 0 && ` · ${count(status.dropped)} packets missed under load`}
            <br />
            Encrypted connections show where they went, not what was said.
          </footer>
        </main>
      </div>
      )}
    </div>
  )
}

function Logo() {
  return (
    <span className="logo">
      <svg viewBox="0 0 32 32" aria-hidden="true">
        <rect width="32" height="32" rx="8" />
        <path d="M8 11h16M16 11v12" />
        <circle cx="24" cy="22" r="2.6" />
      </svg>
      Tattle
    </span>
  )
}

function Tile({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="tile" title={hint}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  )
}

function TabButton(p: { id: Tab; tab: Tab; set: (t: Tab) => void; label: string; n?: number }) {
  return (
    <button role="tab" aria-selected={p.tab === p.id} onClick={() => p.set(p.id)}>
      {p.label}
      {p.n !== undefined && <span className="badge">{count(p.n)}</span>}
    </button>
  )
}

function Amounts({ sent, received }: { sent: number; received: number }) {
  return (
    <span className="amounts">
      <span title="Sent by your machine">↑ {bytes(sent)}</span>
      <span title="Received by your machine">↓ {bytes(received)}</span>
    </span>
  )
}

function SplitBar({ split, info }: { split: Split; info: SettingsInfo }) {
  const { internet, local } = split
  const networks = [...info.detected, ...info.settings.localNets]
  const total = internet.bytes + local.bytes
  if (total === 0) return <p className="empty">No traffic in this time range.</p>
  const pct = (s: Share) => Math.round((s.bytes / total) * 100)
  const rows = [
    { key: 'internet', title: 'Internet', hint: 'Left or entered your home network', share: internet },
    { key: 'local', title: 'Inside your network', hint: 'Stayed between your own devices', share: local },
  ]
  return (
    <div className="split">
      <div className="split-bar" role="img" aria-label={`${pct(internet)}% internet, ${pct(local)}% inside your network`}>
        {rows.map((r) => r.share.bytes > 0 && <i key={r.key} className={r.key} style={{ flexGrow: r.share.bytes }} />)}
      </div>
      <div className="split-legend">
        {rows.map((r) => (
          <div key={r.key}>
            <span className="split-title">
              <i className={`swatch ${r.key}`} />
              {r.title}
            </span>
            <strong>
              {bytes(r.share.bytes)} <small>{pct(r.share)}%</small>
            </strong>
            <span className="split-hint">
              {plural(r.share.places, 'place')} · {r.hint}
            </span>
          </div>
        ))}
      </div>
      <p className="split-note">
        Counted as your network:{' '}
        {[...networks, ...(info.settings.privateIsLocal ? ['any private address (such as 192.168.x.x or 10.x.x.x)'] : [])].join(
          ', ',
        ) || 'only your own VMs and containers'}
        . <a href="#/settings">Change</a>
      </p>
    </div>
  )
}

function Empty({ children }: { children: string }) {
  return <p className="empty">{children}</p>
}

function PeerList(p: { peers: Peer[]; names: Map<string, Machine>; now: number; showMachines: boolean }) {
  if (p.peers.length === 0) {
    return <Empty>Nothing here for this time range. Tattle only sees traffic from the moment it was started.</Empty>
  }
  return (
    <ul className="rows">
      {p.peers.map((peer) => {
        const other = p.names.get(peer.peerId)
        const services = peer.services.map(service)
        const local = peer.local
        return (
          <li key={peer.direction + peer.name}>
            <div className="row-main">
              <span className="row-title">
                {other ? other.name : peer.name}
                {other && <span className="tag">Your {KIND_LABEL[other.kind].toLowerCase()}</span>}
                <ScopeTag local={local} incoming={peer.direction === 'in'} />
                {services.some((s) => s.plain) && <span className="tag warn">Not encrypted</span>}
              </span>
              <span className="row-sub">
                {services
                  .slice(0, 3)
                  .map((s) => s.label)
                  .join(', ')}
                {services.length > 3 && ` and ${services.length - 3} more`}
                {other || peer.name !== peer.ip ? ` · ${peer.ip}` : ''}
                {peer.ipCount > 1 && ` and ${peer.ipCount - 1} more`}
              </span>
              {p.showMachines && (
                <span className="row-who">
                  {peer.direction === 'out' ? 'Contacted by ' : 'Connected to '}
                  {peer.machines.map((id) => p.names.get(id)?.name ?? id).join(', ')}
                </span>
              )}
            </div>
            <div className="row-side">
              <Amounts sent={peer.sent} received={peer.received} />
              <span className="when">
                {plural(peer.conns, 'time')} · {ago(peer.lastSeen, p.now)}
              </span>
            </div>
          </li>
        )
      })}
    </ul>
  )
}

function LogList(p: { rows: Conversation[]; names: Map<string, Machine>; now: number }) {
  if (p.rows.length === 0) {
    return <Empty>Nothing matches. Try a longer time range or a different search.</Empty>
  }
  return (
    <ul className="rows">
      {p.rows.map((c) => {
        const client = p.names.get(c.clientId)?.name ?? c.clientIp
        const server = c.domain || p.names.get(c.serverId)?.name || c.serverIp
        const svc = service(`${c.port}/${c.proto}`)
        // Amounts are shown from the side of the machine being tracked.
        const mine = c.clientId !== ''
        return (
          <li key={[c.clientIp, c.serverIp, c.port, c.proto, c.domain].join('|')}>
            <div className="row-main">
              <span className="row-title">
                {client} <span className="arrow">→</span> {server}
                <ScopeTag local={c.local} incoming={!mine} />
                {svc.plain && <span className="tag warn">Not encrypted</span>}
              </span>
              <span className="row-sub">
                {svc.label} · {c.clientIp} to {c.serverIp}, port {c.port}
              </span>
            </div>
            <div className="row-side">
              <Amounts sent={mine ? c.sent : c.received} received={mine ? c.received : c.sent} />
              <span className="when">
                {plural(c.conns, 'time')} · {ago(c.lastSeen, p.now)}
              </span>
            </div>
          </li>
        )
      })}
    </ul>
  )
}
