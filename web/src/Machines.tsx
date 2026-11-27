import { useState } from 'react'
import { peerLabel, type Devices, type Machine, type Peer, type Split, type Timeline } from './api'
import { Spark, TrafficChart } from './Chart'
import { ago, bytes, count, KIND_LABEL, service, totals } from './format'
import { Icon } from './icons'
import { Empty } from './parts'
import { formatHash, type View } from './url'

type Kind = 'all' | Machine['kind']

const FILTERS: { kind: Kind; label: string }[] = [
  { kind: 'all', label: 'All' },
  { kind: 'vm', label: 'VMs' },
  { kind: 'ct', label: 'Containers' },
  { kind: 'host', label: 'Host' },
]

/** Every VM, container and the host in one table, with details beside it. */
export function MachinesPage(p: {
  machines: Machine[]
  timeline: Timeline
  peers: Peer[]
  split: Split
  devices: Devices
  view: View
  go: (patch: Partial<View>, replace?: boolean) => void
  now: number
  stale: boolean
  rangePhrase: string
}) {
  const [kind, setKind] = useState<Kind>('all')
  const [filter, setFilter] = useState('')
  const q = filter.trim().toLowerCase()
  const rows = p.machines.filter(
    (m) =>
      (kind === 'all' || m.kind === kind) &&
      (!q || m.name.toLowerCase().includes(q) || m.ips.some((ip) => ip.includes(q)) || String(m.vmid) === q),
  )
  const selected = p.machines.find((m) => m.id === p.view.machine)
  const open = (id: string) => p.go({ machine: id === p.view.machine ? '' : id })

  return (
    <div className="page machines-page" data-drawer={selected ? true : undefined}>
      <div className="page-head">
        <div>
          <h1>Machines</h1>
          <p>Read from Proxmox. Nothing is installed inside the guests.</p>
        </div>
      </div>
      <div className="machines-layout">
        <div className="machines-main">
          <div className="toolbar">
            <div className="chips-filter" role="group" aria-label="Show">
              {FILTERS.map((f) => {
                const n = p.machines.filter((m) => f.kind === 'all' || m.kind === f.kind).length
                if (f.kind !== 'all' && n === 0) return null
                return (
                  <button key={f.kind} aria-pressed={kind === f.kind} onClick={() => setKind(f.kind)}>
                    {f.label} <span>{n}</span>
                  </button>
                )
              })}
            </div>
            <label className="search">
              <Icon name="search" />
              <input
                type="search"
                placeholder="Filter by name or address"
                aria-label="Filter machines"
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
              />
            </label>
          </div>
          <div className="card flush table-wrap">
            <table className="table machine-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Address</th>
                  <th>Activity</th>
                  <th className="num">Received</th>
                  <th className="num">Sent</th>
                  <th className="num">Places</th>
                  <th className="num">Last traffic</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((m) => (
                  <tr key={m.id} aria-selected={m.id === p.view.machine} onClick={() => open(m.id)}>
                    <td>
                      <button type="button" className="machine-cell" onClick={(e) => {
                          e.stopPropagation()
                          open(m.id)
                        }}>
                        <span className="machine-icon" data-quiet={!m.lastSeen || undefined}>
                          <Icon name={m.kind} />
                        </span>
                        <span>
                          <b>{m.name}</b>
                          <small>
                            {m.kind === 'host' ? 'Proxmox host' : `${KIND_LABEL[m.kind]} ${m.vmid}`}
                            {!m.present && ' · no longer exists'}
                          </small>
                        </span>
                      </button>
                    </td>
                    <td className="mono">{m.ips[0] ?? '–'}</td>
                    <td>
                      <Spark values={m.spark} />
                    </td>
                    <td className="num">{bytes(m.received)}</td>
                    <td className="num">{bytes(m.sent)}</td>
                    <td className="num">{count(m.destinations)}</td>
                    <td className="num when-cell">{m.lastSeen ? ago(m.lastSeen, p.now) : 'None'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            {rows.length === 0 && <Empty>No machine matches.</Empty>}
          </div>
        </div>

        {selected && (
          <MachineDrawer {...p} machine={selected} onClose={() => p.go({ machine: '' })} />
        )}
      </div>
    </div>
  )
}

function MachineDrawer(p: {
  machine: Machine
  machines: Machine[]
  devices: Devices
  timeline: Timeline
  peers: Peer[]
  split: Split
  view: View
  now: number
  stale: boolean
  rangePhrase: string
  onClose: () => void
}) {
  const m = p.machine
  const [tab, setTab] = useState<'out' | 'in'>('out')
  const { sent, received } = totals(p.timeline)
  const total = p.split.internet.bytes + p.split.local.bytes
  const internetShare = total ? Math.round((p.split.internet.bytes / total) * 100) : 0
  const list = p.peers
    .filter((x) => x.direction === tab)
    .sort((a, b) => b.sent + b.received - (a.sent + a.received))
    .slice(0, 12)
  const names = new Map(p.machines.map((x) => [x.id, x]))
  const outN = p.peers.filter((x) => x.direction === 'out').length
  const inN = p.peers.filter((x) => x.direction === 'in').length

  return (
    <aside className="drawer card" aria-label={`${m.name} details`} data-stale={p.stale || undefined}>
      <div className="drawer-head">
        <span className="machine-icon large">
          <Icon name={m.kind} />
        </span>
        <div>
          <h2>{m.name}</h2>
          <div className="drawer-tags">
            <span className="tag">{m.kind === 'host' ? 'Proxmox host' : `${KIND_LABEL[m.kind]} ${m.vmid}`}</span>
            <span className="status" data-on={m.lastSeen > 0 || undefined}>
              <i aria-hidden="true" />
              {m.lastSeen ? `Active ${ago(m.lastSeen, p.now)}` : `Quiet for the last ${p.rangePhrase}`}
            </span>
          </div>
        </div>
        <button type="button" className="icon-button" aria-label="Close details" onClick={p.onClose}>
          <Icon name="close" />
        </button>
      </div>

      {p.stale ? (
        <Empty>Loading…</Empty>
      ) : (
        <>
          <div className="mini-tiles">
            <div>
              <span>Received</span>
              <b>{bytes(received)}</b>
            </div>
            <div>
              <span>Sent</span>
              <b>{bytes(sent)}</b>
            </div>
            <div>
              <span>Internet</span>
              <b>{internetShare}%</b>
            </div>
          </div>

          <TrafficChart data={p.timeline} range={p.view.range} height={140} legend={false} />

          <dl className="facts drawer-facts">
            <div>
              <dt>{m.ips.length === 1 ? 'Address' : 'Addresses'}</dt>
              <dd className="mono">{m.ips.join(', ') || 'Not known yet'}</dd>
            </div>
            <div>
              <dt>Hardware address</dt>
              <dd className="mono">{m.macs.join(', ') || 'Not known'}</dd>
            </div>
            <div>
              <dt>Runs on</dt>
              <dd>{m.node || 'Not known'}</dd>
            </div>
            <div>
              <dt>Connections started</dt>
              <dd>{count(m.conns)}</dd>
            </div>
          </dl>

          <div className="tabs" role="tablist">
            <button role="tab" aria-selected={tab === 'out'} onClick={() => setTab('out')}>
              Places <span className="badge">{count(outN)}</span>
            </button>
            <button role="tab" aria-selected={tab === 'in'} onClick={() => setTab('in')}>
              Callers <span className="badge">{count(inN)}</span>
            </button>
          </div>
          {list.length === 0 ? (
            <Empty>{tab === 'out' ? 'It contacted nothing' : 'Nothing connected to it'} in the last {p.rangePhrase}.</Empty>
          ) : (
            <ul className="drawer-list">
              {list.map((x) => (
                <li key={x.direction + x.name}>
                  <a href={formatHash({ ...p.view, page: 'dashboard', peer: x.name, dir: x.direction })}>
                    <span>
                      <b>{peerLabel(x, names, p.devices)}</b>
                      <small>
                        {x.services.map((s) => service(s).label).slice(0, 2).join(', ')}
                        {x.local ? ' · your network' : ' · internet'}
                      </small>
                    </span>
                    <span className="num">{bytes(x.sent + x.received)}</span>
                  </a>
                </li>
              ))}
            </ul>
          )}

          <a className="button primary" href={formatHash({ ...p.view, page: 'dashboard' })}>
            Open {m.name} on the dashboard
          </a>
        </>
      )}
    </aside>
  )
}
