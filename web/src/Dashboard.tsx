import { useEffect, useState } from 'react'
import {
  api,
  deviceLabel,
  peerLabel,
  type Devices,
  type Fresh,
  type Health,
  type Machine,
  type Peer,
  type Scope,
  type SettingsInfo,
  type Share,
  type Split,
  type Status,
  type Timeline,
} from './api'
import { TrafficChart } from './Chart'
import { ago, bytes, count, KIND_LABEL, plural, service, totals, YOURS_LABEL } from './format'
import { StateBadge } from './Health'
import { Icon } from './icons'
import { WhatsNew } from './News'
import { Empty, Tile } from './parts'
import { PeerList } from './Peers'
import { formatHash, type Sort, type Tab, type View } from './url'

export type Data = {
  machines: Machine[]
  timeline: Timeline
  peers: Peer[]
  split: Split
  fresh: Fresh
  devices: Devices
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

export function Dashboard(p: {
  data: Data
  status: Status
  info: SettingsInfo
  scope: Scope
  view: View
  go: (patch: Partial<View>, replace?: boolean) => void
  now: number
  stale: boolean
  rangePhrase: string
  moreNew: boolean
  onMoreNew: (more: boolean) => void
  names: Map<string, Machine>
  selected: Machine | undefined
}) {
  const { data, view, go, names, selected, now } = p
  const { tab, sort } = view

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

  const matches = (x: Peer) => {
    if (!query) return true
    const q = query.toLowerCase()
    return (
      x.name.toLowerCase().includes(q) ||
      x.ip.includes(q) ||
      (names.get(x.peerId)?.name.toLowerCase().includes(q) ?? false) ||
      deviceLabel(data.devices, x.ip).toLowerCase().includes(q) ||
      (x.owner?.name.toLowerCase().includes(q) ?? false)
    )
  }
  const order = (a: Peer, b: Peer) =>
    sort === 'data' ? b.sent + b.received - (a.sent + a.received) : b.lastSeen - a.lastSeen
  const outgoing = data.peers.filter((x) => x.direction === 'out')
  const incoming = data.peers.filter((x) => x.direction === 'in')
  const shown = (tab === 'out' ? outgoing : incoming).filter(matches).sort(order)

  const { sent, received } = totals(data.timeline)
  const active = data.machines.filter((m) => m.lastSeen > 0).length
  const fromInternet = incoming.filter((x) => !x.local).length
  const firsts = data.fresh.firsts.length + data.fresh.more

  return (
    <div className="page dashboard" data-stale={p.stale || undefined} aria-busy={p.stale}>
      <div className="page-head">
        <div>
          <h1>
            {selected ? selected.name : 'Dashboard'}
            {p.stale && (
              <span className="loading" role="status">
                Loading the last {p.rangePhrase}…
              </span>
            )}
          </h1>
          <p>
            {selected
              ? [
                  selected.kind === 'host' ? 'The Proxmox host itself' : `${KIND_LABEL[selected.kind]} ${selected.vmid}`,
                  ...selected.ips.slice(0, 2),
                  selected.present ? '' : 'no longer exists',
                ]
                  .filter(Boolean)
                  .join(' · ')
              : `Everything your VMs, containers and the host did on the network in the last ${p.rangePhrase}.`}
          </p>
        </div>
        <label className="picker">
          <span className="sr-only">Machine</span>
          <select value={view.machine} onChange={(e) => go({ machine: e.target.value })}>
            <option value="">All machines</option>
            {data.machines.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name} ({KIND_LABEL[m.kind]})
              </option>
            ))}
          </select>
        </label>
      </div>

      <section className="stats" aria-label="Figures">
        {selected ? (
          <Tile
            label="Last traffic"
            value={selected.lastSeen ? ago(selected.lastSeen, now) : 'None'}
            note={selected.present ? `${KIND_LABEL[selected.kind]} on ${selected.node}` : 'No longer exists'}
            tone={selected.lastSeen ? 'good' : undefined}
          />
        ) : (
          <Tile
            label="Machines"
            value={`${active} / ${data.machines.length}`}
            note={active === data.machines.length ? 'All active' : `${data.machines.length - active} quiet`}
            tone={active === data.machines.length ? 'good' : undefined}
          />
        )}
        <Tile
          label="Places contacted"
          value={count(selected ? selected.destinations : outgoing.length)}
          note={`${plural(data.split.internet.places, 'place')} on the internet`}
        />
        <Tile
          label="Callers"
          value={count(incoming.length)}
          hint="Others that connected in"
          note={fromInternet > 0 ? `${fromInternet} from the internet` : 'None from the internet'}
          tone={fromInternet > 0 ? 'warn' : undefined}
        />
        <Tile label="Received" value={bytes(received)} note={`in the last ${p.rangePhrase}`} />
        <Tile label="Sent" value={bytes(sent)} note={`in the last ${p.rangePhrase}`} />
        <Tile
          label="New"
          value={count(firsts)}
          note={firsts === 1 ? 'first contact' : 'first contacts'}
          tone={firsts > 0 ? 'warn' : undefined}
        />
      </section>

      <div className="grid">
        <section className="card span-8">
          <div className="card-head">
            <h2>Traffic</h2>
            <div className="card-figures">
              <span>
                <i className="swatch received" />
                Received <b>{bytes(received)}</b>
              </span>
              <span>
                <i className="swatch sent" />
                Sent <b>{bytes(sent)}</b>
              </span>
            </div>
          </div>
          <TrafficChart data={data.timeline} range={view.range} legend={false} />
        </section>

        <HealthCard state={p.status.health} href={formatHash({ ...view, page: 'health', peer: '' })} />

        <WhatsNew
          className="span-8"
          fresh={data.fresh}
          names={names}
          devices={data.devices}
          now={now}
          view={view}
          rangePhrase={p.rangePhrase}
          expanded={p.moreNew}
          onExpand={p.onMoreNew}
        />

        <section className="card span-4">
          <div className="card-head">
            <h2>Where the traffic went</h2>
          </div>
          <SplitRing
            split={data.split}
            info={p.info}
            settingsHref={formatHash({ ...view, page: 'settings', peer: '' })}
            scope={p.scope}
            onScope={(scope) => go({ scope })}
          />
        </section>

        {selected ? (
          <MachineCard machine={selected} view={view} />
        ) : (
          <TopMachines machines={data.machines} view={view} now={now} />
        )}

        <TopPlaces peers={outgoing} names={names} devices={data.devices} view={view} />

        <section className="card span-12">
          <div className="toolbar">
            <div className="tabs" role="tablist">
              <TabButton id="out" tab={tab} set={(t) => go({ tab: t })} label="Reaching out" n={outgoing.length} />
              <TabButton id="in" tab={tab} set={(t) => go({ tab: t })} label="Coming in" n={incoming.length} />
            </div>
            <label className="search">
              <Icon name="search" />
              <input
                type="search"
                placeholder="Search a name or address"
                aria-label="Search"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </label>
            <select aria-label="Sort" value={sort} onChange={(e) => go({ sort: e.target.value as Sort }, true)}>
              <option value="recent">Most recent first</option>
              <option value="data">Most data first</option>
            </select>
          </div>
          <p className="explain">
            {tab === 'out' && 'Websites, services and devices that your machines started a conversation with.'}
            {tab === 'in' && 'Devices and outside addresses that started a conversation with your machines.'} Choose
            one for the details.
          </p>
          <PeerList peers={shown} names={names} devices={data.devices} now={now} showMachines={!selected} view={view} />
        </section>
      </div>

      <footer>
        Tattle {p.status.version} · keeps {p.status.retentionDays} days of history, the last {p.status.detailDays} minute by
        minute
        {p.status.dropped > 0 && ` · ${count(p.status.dropped)} packets missed under load`}
        <br />
        Encrypted connections show where they went, not what was said.
      </footer>
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

/** Tattle's own checks, summed up, with the way to the full page. */
function HealthCard(p: { state: Status['health']; href: string }) {
  const [health, setHealth] = useState<Health | null>(null)
  useEffect(() => {
    let current = true
    const load = () => {
      if (document.hidden) return
      api
        .health()
        .then((h) => current && setHealth(h))
        .catch(() => {})
    }
    load()
    const id = setInterval(load, 15000)
    return () => {
      current = false
      clearInterval(id)
    }
  }, [])
  const state = health?.state ?? p.state
  const troubled = health?.checks.filter((c) => c.state === 'warn' || c.state === 'bad') ?? []
  const working = health?.checks.filter((c) => c.state === 'ok').length ?? 0
  const unused = health?.checks.filter((c) => c.state === 'off').length ?? 0
  return (
    <section className="card span-4 health-card">
      <div className="card-head">
        <h2>Tattle health</h2>
        <a href={p.href}>Details</a>
      </div>
      <div className="health-sum" data-state={state}>
        <span className="health-disc" aria-hidden="true">
          <Icon name={state === 'ok' || state === 'off' ? 'check' : 'alert'} />
        </span>
        <div>
          <strong>
            {troubled.length === 0
              ? 'Everything is working'
              : troubled.length === 1
                ? troubled[0].title
                : `${troubled.length} things need attention`}
          </strong>
          {health && (
            <span>
              {[`${working} working`, unused > 0 ? `${unused} not in use` : '', `up ${health.uptime}`].filter(Boolean).join(' · ')}
            </span>
          )}
        </div>
      </div>
      {health && (
        <ul className="checks">
          {health.checks.map((c) => (
            <li key={c.id}>
              <span>{c.title}</span>
              <StateBadge state={c.state} />
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

const RING = 2 * Math.PI * 52

/**
 * How traffic divides between the internet and the home network, which is
 * also the control for hiding either side from the rest of the page.
 */
function SplitRing(p: { split: Split; info: SettingsInfo; settingsHref: string; scope: Scope; onScope: (s: Scope) => void }) {
  const { internet, local } = p.split
  const networks = [...p.info.detected, ...p.info.settings.localNets]
  const total = internet.bytes + local.bytes
  if (total === 0 && p.scope === 'all') return <Empty>No traffic in this time range.</Empty>
  const pct = (s: Share) => (total === 0 ? 0 : Math.round((s.bytes / total) * 100))
  const arc = total === 0 ? 0 : (internet.bytes / total) * RING
  // Hiding a side means showing only the other one.
  const sides: { key: Scope; title: string; phrase: string; share: Share; hidden: boolean; hide: Scope }[] = [
    { key: 'internet', title: 'Internet', phrase: 'Traffic to and from the internet', share: internet, hidden: p.scope === 'local', hide: 'local' },
    { key: 'local', title: 'Your network', phrase: 'Traffic that stayed inside your network', share: local, hidden: p.scope === 'internet', hide: 'internet' },
  ]
  const hidden = sides.find((s) => s.hidden)
  return (
    <div className="split">
      <div className="split-body">
        <svg className="ring" viewBox="0 0 132 132" role="img" aria-label={`${pct(internet)}% internet, ${pct(local)}% inside your network`}>
          <circle className="ring-local" cx="66" cy="66" r="52" data-off={p.scope === 'internet' || undefined} />
          {arc > 0 && (
            <circle
              className="ring-internet"
              cx="66"
              cy="66"
              r="52"
              strokeDasharray={`${arc} ${RING}`}
              transform="rotate(-90 66 66)"
              data-off={p.scope === 'local' || undefined}
            />
          )}
          <text x="66" y="64" className="ring-value">
            {bytes(total)}
          </text>
          <text x="66" y="82" className="ring-label">
            in all
          </text>
        </svg>
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
              <span className="split-hint">{plural(s.share.places, 'place')}</span>
            </div>
          ))}
        </div>
      </div>
      <div className="split-actions">
        {hidden ? (
          <button className="button" aria-label={`Show ${hidden.phrase.toLowerCase()} again`} onClick={() => p.onScope('all')}>
            Show {hidden.key === 'internet' ? 'internet' : 'local'} traffic again
          </button>
        ) : (
          sides.map((s) => (
            <button key={s.key} className="button" aria-label={`Hide ${s.phrase.toLowerCase()}`} onClick={() => p.onScope(s.hide)}>
              Hide {s.key === 'internet' ? 'internet' : 'local'}
            </button>
          ))
        )}
      </div>
      {hidden && (
        <p className="split-hidden" role="status">
          {hidden.phrase} is hidden from the figures, chart and lists.
        </p>
      )}
      <p className="split-note">
        Counted as your network:{' '}
        {[...networks, ...(p.info.settings.privateIsLocal ? ['any private address'] : [])].join(', ') ||
          'only your own VMs and containers'}
        . <a href={p.settingsHref}>Change</a>
      </p>
    </div>
  )
}

/** Every machine, busiest first, with how its traffic divides. */
function TopMachines(p: { machines: Machine[]; view: View; now: number }) {
  const list = [...p.machines].sort((a, b) => b.sent + b.received - (a.sent + a.received))
  const max = Math.max(1, ...list.map((m) => m.sent + m.received))
  return (
    <section className="card span-6">
      <div className="card-head">
        <h2>Busiest machines</h2>
        <a href={formatHash({ ...p.view, page: 'machines', peer: '' })}>All machines</a>
      </div>
      <ul className="bars">
        {list.map((m) => {
          const total = m.sent + m.received
          return (
            <li key={m.id}>
              <a href={formatHash({ ...p.view, machine: m.id })}>
                <span className="bars-name">
                  <Icon name={m.kind} />
                  <span>
                    <b>{m.name}</b>
                    <small>
                      {KIND_LABEL[m.kind]} · {m.lastSeen ? ago(m.lastSeen, p.now) : 'no traffic'}
                    </small>
                  </span>
                </span>
                <span className="bars-track" aria-hidden="true">
                  <i className="received" style={{ width: `${(m.received / max) * 100}%` }} />
                  <i className="sent" style={{ width: `${(m.sent / max) * 100}%` }} />
                </span>
                <span className="bars-value">{bytes(total)}</span>
              </a>
            </li>
          )
        })}
      </ul>
    </section>
  )
}

/** The machine being looked at, in brief. */
function MachineCard(p: { machine: Machine; view: View }) {
  const m = p.machine
  return (
    <section className="card span-6">
      <div className="card-head">
        <h2>About {m.name}</h2>
        <a href={formatHash({ ...p.view, machine: '' })}>Show all machines</a>
      </div>
      <dl className="facts grid-facts">
        <div>
          <dt>Kind</dt>
          <dd>{m.kind === 'host' ? 'The Proxmox host' : `${KIND_LABEL[m.kind]} ${m.vmid}`}</dd>
        </div>
        <div>
          <dt>Runs on</dt>
          <dd>{m.node || 'Not known'}</dd>
        </div>
        <div>
          <dt>{m.ips.length === 1 ? 'Address' : 'Addresses'}</dt>
          <dd className="mono">{m.ips.join(', ') || 'Not known yet'}</dd>
        </div>
        <div>
          <dt>Hardware address</dt>
          <dd className="mono">{m.macs.join(', ') || 'Not known'}</dd>
        </div>
        <div>
          <dt>Connections started</dt>
          <dd>{count(m.conns)}</dd>
        </div>
        <div>
          <dt>Places contacted</dt>
          <dd>{count(m.destinations)}</dd>
        </div>
      </dl>
      <a className="button" href={formatHash({ ...p.view, page: 'machines', peer: '' })}>
        Open in Machines
      </a>
    </section>
  )
}

/** The places that took the most data, with what kind of place each is. */
function TopPlaces(p: { peers: Peer[]; names: Map<string, Machine>; devices: Devices; view: View }) {
  const list = [...p.peers].sort((a, b) => b.sent + b.received - (a.sent + a.received)).slice(0, 7)
  const max = Math.max(1, ...list.map((x) => x.sent + x.received))
  return (
    <section className="card span-6">
      <div className="card-head">
        <h2>Most data</h2>
        <span className="card-sub">Places contacted</span>
      </div>
      {list.length === 0 ? (
        <Empty>Nothing contacted in this time range.</Empty>
      ) : (
        <ul className="bars">
          {list.map((x) => {
            const other = p.names.get(x.peerId)
            const svc = x.services[0] ? service(x.services[0]).label : ''
            return (
              <li key={x.name}>
                <a href={formatHash({ ...p.view, peer: x.name, dir: x.direction })}>
                  <span className="bars-name">
                    <span className="monogram" data-local={x.local || undefined}>
                      {peerLabel(x, p.names, p.devices).replace(/^www\./, '').charAt(0).toUpperCase()}
                    </span>
                    <span>
                      <b>{peerLabel(x, p.names, p.devices)}</b>
                      <small>
                        {other ? YOURS_LABEL[other.kind] : x.local ? 'Your network' : x.owner?.name || 'Internet'}
                        {svc && ` · ${svc}`}
                      </small>
                    </span>
                  </span>
                  <span className="bars-track" aria-hidden="true">
                    <i className="received" style={{ width: `${(x.received / max) * 100}%` }} />
                    <i className="sent" style={{ width: `${(x.sent / max) * 100}%` }} />
                  </span>
                  <span className="bars-value">{bytes(x.sent + x.received)}</span>
                </a>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
