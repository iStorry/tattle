import { useEffect, useMemo, useState } from 'react'
import {
  api,
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
import { ago, bytes, count, KIND_LABEL, plural, service, YOURS_LABEL } from './format'
import { PeerPage } from './Detail'
import { Amounts, Empty, Logo, ScopeTag, Tile } from './parts'
import { SettingsPage } from './Settings'
import { formatHash, RANGES, useView, type Sort, type Tab, type View } from './url'

type Data = {
  machines: Machine[]
  timeline: Timeline
  peers: Peer[]
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

export default function App({ canSignOut, onSignOut }: { canSignOut: boolean; onSignOut: () => void }) {
  // What is being looked at comes from the address bar, so it can be
  // bookmarked, shared and stepped through with the back button.
  const [view, go] = useView()
  const { range, machine, tab, sort } = view
  const route = view.page
  const setRange = (range: number) => go({ range })
  const setTab = (tab: Tab) => go({ tab })
  const setSort = (sort: Sort) => go({ sort }, true)
  const setScope = (scope: Scope) => go({ scope })

  const [info, setInfo] = useState<SettingsInfo | null>(null)
  // Unless the address says otherwise, the saved settings decide what the
  // dashboard opens with, and they are not known until they have loaded.
  const scope: Scope | null = info ? (view.scope ?? info.settings.show) : null

  // The search box keeps its own text while typing, and the address
  // follows once the typing pauses.
  const [search, setSearch] = useState(view.q)
  const query = useDebounced(search.trim(), 250)
  useEffect(() => {
    go({ q: query }, true)
  }, [query, go])
  const [seenQ, setSeenQ] = useState(view.q)
  if (view.q !== seenQ) {
    // The address changed underneath, for example with the back button.
    setSeenQ(view.q)
    if (view.q !== search.trim()) setSearch(view.q)
  }

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
        const [st, machines, timeline, peers, split] = await Promise.all([
          api.status(),
          api.machines(range, scope),
          api.timeline(range, machine, scope),
          api.peers(range, machine, scope),
          api.split(range, machine),
        ])
        if (!current) return
        setStatus(st)
        setNow(st.now)
        setData({ machines, timeline, peers, split })
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
  }, [range, machine, scope, info])

  const names = useMemo(() => new Map(data?.machines.map((m) => [m.id, m]) ?? []), [data])
  const selected = machine ? names.get(machine) : undefined

  // An address naming a machine that does not exist shows everything.
  const unknownMachine = data !== null && machine !== '' && !selected
  useEffect(() => {
    if (unknownMachine) go({ machine: '' }, true)
  }, [unknownMachine, go])

  // The tab title says what is open, which helps with bookmarks and history.
  // A destination that is one of the owner's machines is titled by its name.
  const peerMachine = data?.machines.find((m) => m.ips.includes(view.peer))
  const pageTitle = route === 'settings' ? 'Settings' : (peerMachine?.name ?? view.peer) || (selected?.name ?? '')
  useEffect(() => {
    document.title = pageTitle ? `${pageTitle} · Tattle` : 'Tattle'
  }, [pageTitle])

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
        <a className="nav-link" href={formatHash({ ...view, page: route === 'settings' ? 'dashboard' : 'settings' })}>
          {route === 'settings' ? 'Dashboard' : 'Settings'}
        </a>
        {canSignOut && (
          <button className="nav-link" onClick={onSignOut}>
            Sign out
          </button>
        )}
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
          canChangePassword={canSignOut}
          backHref={formatHash({ ...view, page: 'dashboard' })}
          onSaved={(saved) => {
            setInfo(saved)
            // Follow the newly saved choice, not one left in the address.
            go({ scope: null }, true)
          }}
        />
      ) : (
      <div className="layout">
        <nav className="machines" aria-label="Machines">
          <h2>Machines</h2>
          <a className="machine" aria-current={machine === ''} href={formatHash({ ...view, machine: '' })}>
            <span className="machine-name">All machines</span>
            <span className="machine-meta">
              {activeMachines} of {data.machines.length} active
            </span>
          </a>
          {data.machines.map((m) => (
            <a key={m.id} className="machine" aria-current={machine === m.id} href={formatHash({ ...view, machine: m.id })}>
              <span className="machine-name">
                {m.name}
                <span className="kind">{KIND_LABEL[m.kind]}</span>
              </span>
              <span className="machine-meta">
                {m.lastSeen ? `${bytes(m.sent + m.received)} · ${ago(m.lastSeen, now)}` : 'No traffic'}
              </span>
              <Spark values={m.spark} />
            </a>
          ))}
        </nav>

        {view.peer ? (
          <PeerPage view={view} names={names} now={now} />
        ) : (
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
          </div>

          <section className="card">
            <h2>Where the traffic went</h2>
            <SplitBar
              split={data.split}
              info={info}
              settingsHref={formatHash({ ...view, page: 'settings' })}
              scope={scope}
              onScope={setScope}
            />
          </section>

          <section className="tiles">
            <Tile label="Places contacted" value={count(selected ? selected.destinations : outgoing.length)} />
            <Tile label="Callers" value={count(incoming.length)} hint="Others that connected in" />
            <Tile label="Sent" value={bytes(sent)} />
            <Tile label="Received" value={bytes(received)} />
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
              </div>
              <input
                type="search"
                placeholder="Search a name or address"
                aria-label="Search"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
              <select aria-label="Sort" value={sort} onChange={(e) => setSort(e.target.value as Sort)}>
                <option value="recent">Most recent first</option>
                <option value="data">Most data first</option>
              </select>
            </div>

            <p className="explain">
              {tab === 'out' && 'Websites, services and devices that your machines started a conversation with.'}
              {tab === 'in' && 'Devices and outside addresses that started a conversation with your machines.'} Choose
              one for the details.
            </p>

            <PeerList peers={shown} names={names} now={now} showMachines={!selected} view={view} />
          </section>

          <footer>
            Tattle {status.version} · keeps {status.retentionDays} days of history
            {status.dropped > 0 && ` · ${count(status.dropped)} packets missed under load`}
            <br />
            Encrypted connections show where they went, not what was said.
          </footer>
        </main>
        )}
      </div>
      )}
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

/**
 * Shows how traffic divides between the internet and the home network, and
 * is also the control for hiding either side from the rest of the page.
 */
function SplitBar(p: { split: Split; info: SettingsInfo; settingsHref: string; scope: Scope; onScope: (s: Scope) => void }) {
  const { internet, local } = p.split
  const networks = [...p.info.detected, ...p.info.settings.localNets]
  const total = internet.bytes + local.bytes
  if (total === 0 && p.scope === 'all') return <p className="empty">No traffic in this time range.</p>
  const pct = (s: Share) => (total === 0 ? 0 : Math.round((s.bytes / total) * 100))
  // Hiding a side means showing only the other one.
  const sides: { key: Scope; title: string; phrase: string; hint: string; share: Share; hidden: boolean; hide: Scope }[] = [
    {
      key: 'internet',
      title: 'Internet',
      phrase: 'Traffic to and from the internet',
      hint: 'Left or entered your home network',
      share: internet,
      hidden: p.scope === 'local',
      hide: 'local',
    },
    {
      key: 'local',
      title: 'Inside your network',
      phrase: 'Traffic that stayed inside your network',
      hint: 'Stayed between your own devices',
      share: local,
      hidden: p.scope === 'internet',
      hide: 'internet',
    },
  ]
  const hidden = sides.find((s) => s.hidden)
  return (
    <div className="split">
      <div className="split-bar" role="img" aria-label={`${pct(internet)}% internet, ${pct(local)}% inside your network`}>
        {sides.map(
          (s) => s.share.bytes > 0 && <i key={s.key} className={s.hidden ? `${s.key} off` : s.key} style={{ flexGrow: s.share.bytes }} />,
        )}
      </div>
      <div className="split-legend">
        {sides.map((s) => (
          <div key={s.key} className={s.hidden ? 'off' : undefined}>
            <span className="split-title">
              <i className={`swatch ${s.key}`} />
              {s.title}
              {s.hidden && <span className="tag">Hidden</span>}
            </span>
            <strong>
              {bytes(s.share.bytes)} <small>{pct(s.share)}%</small>
            </strong>
            <span className="split-hint">
              {plural(s.share.places, 'place')} · {s.hint}
            </span>
            {s.hidden ? (
              <button className="toggle" aria-label={`Show ${s.phrase.toLowerCase()} again`} onClick={() => p.onScope('all')}>
                Show it again
              </button>
            ) : (
              !hidden && (
                <button className="toggle" aria-label={`Hide ${s.phrase.toLowerCase()}`} onClick={() => p.onScope(s.hide)}>
                  Hide this
                </button>
              )
            )}
          </div>
        ))}
      </div>
      {hidden && (
        <p className="split-hidden" role="status">
          {hidden.phrase} is hidden from the figures, chart and lists below.
        </p>
      )}
      <p className="split-note">
        Counted as your network:{' '}
        {[...networks, ...(p.info.settings.privateIsLocal ? ['any private address (such as 192.168.x.x or 10.x.x.x)'] : [])].join(
          ', ',
        ) || 'only your own VMs and containers'}
        . <a href={p.settingsHref}>Change</a>
      </p>
    </div>
  )
}

function PeerList(p: { peers: Peer[]; names: Map<string, Machine>; now: number; showMachines: boolean; view: View }) {
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
                <a href={formatHash({ ...p.view, peer: peer.name, dir: peer.direction })}>{other ? other.name : peer.name}</a>
                {other && <span className="tag">{YOURS_LABEL[other.kind]}</span>}
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
                  {peer.machines.map((id, i) => (
                    <span key={id}>
                      {i > 0 && ', '}
                      <a href={formatHash({ ...p.view, machine: id })}>{p.names.get(id)?.name ?? id}</a>
                    </span>
                  ))}
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
