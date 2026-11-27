import { useState } from 'react'
import { deviceLabel, type Devices, type Machine, type Peer } from './api'
import { ago, plural, service, times, YOURS_LABEL } from './format'
import { Amounts, Empty, ScopeTag } from './parts'
import { formatHash, type View } from './url'

// The list of places machines contacted, or callers that connected in.

export type ListProps = {
  names: Map<string, Machine>
  devices: Devices
  now: number
  showMachines: boolean
  view: View
}

// Addresses with no name that share a service are folded into one row once
// there are this many. A VPN or video call checking a dozen relays at once
// otherwise fills the list with identical lines.
const FOLD_AT = 4

type Item = { peer: Peer } | { group: Peer[] }

/** Folds the nameless addresses that share a service, keeping list order. */
function fold(peers: Peer[], p: ListProps): Item[] {
  const bare = (peer: Peer) =>
    peer.name === peer.ip && !p.names.get(peer.peerId) && !deviceLabel(p.devices, peer.ip)
  const key = (peer: Peer) => `${peer.direction}|${peer.local}|${peer.services.join(',')}`
  const groups = new Map<string, Peer[]>()
  for (const peer of peers) {
    if (!bare(peer)) continue
    const k = key(peer)
    groups.set(k, [...(groups.get(k) ?? []), peer])
  }
  const items: Item[] = []
  const placed = new Set<string>()
  for (const peer of peers) {
    const group = bare(peer) ? groups.get(key(peer)) : undefined
    if (!group || group.length < FOLD_AT) {
      items.push({ peer })
    } else if (!placed.has(key(peer))) {
      placed.add(key(peer))
      items.push({ group })
    }
  }
  return items
}

export function PeerList(p: ListProps & { peers: Peer[] }) {
  if (p.peers.length === 0) {
    return <Empty>Nothing here for this time range. Tattle only sees traffic from the moment it was started.</Empty>
  }
  return (
    <ul className="rows">
      {fold(p.peers, p).map((item) =>
        'peer' in item ? (
          <PeerRow key={item.peer.direction + item.peer.name} {...p} peer={item.peer} />
        ) : (
          <GroupRow key={'group:' + item.group[0].direction + item.group[0].name} {...p} peers={item.group} />
        ),
      )}
    </ul>
  )
}

/** "A, B and 3 more" */
function some(list: string[], shown = 3): string {
  const head = list.slice(0, shown).join(', ')
  return list.length > shown ? `${head} and ${list.length - shown} more` : head
}

function Machines(p: ListProps & { ids: string[]; direction: Peer['direction'] }) {
  return (
    <span className="row-who">
      {p.direction === 'out' ? 'Contacted by ' : 'Connected to '}
      {p.ids.map((id, i) => (
        <span key={id}>
          {i > 0 && ', '}
          <a href={formatHash({ ...p.view, machine: id })}>{p.names.get(id)?.name ?? id}</a>
        </span>
      ))}
    </span>
  )
}

function GroupRow(p: ListProps & { peers: Peer[] }) {
  const [open, setOpen] = useState(false)
  const first = p.peers[0]
  const services = first.services.map(service)
  const owners = [...new Set(p.peers.map((x) => x.owner?.name).filter((x): x is string => !!x))]
  const machines = [...new Set(p.peers.flatMap((x) => x.machines))]
  const sum = (f: (x: Peer) => number) => p.peers.reduce((n, x) => n + f(x), 0)
  return (
    <li className="group">
      <div className="group-head">
        <div className="row-main">
          <span className="row-title">
            <button type="button" className="group-toggle" aria-expanded={open} onClick={() => setOpen(!open)}>
              <span className="chevron" aria-hidden="true">
                {open ? '▾' : '▸'}
              </span>
              {plural(p.peers.length, 'address', 'addresses')}
            </button>
            {p.peers.some((x) => x.new) && (
              <span className="tag new" title="First seen in this time range">
                New
              </span>
            )}
            <ScopeTag local={first.local} incoming={first.direction === 'in'} />
            {services.some((s) => s.plain) && <span className="tag warn">Not encrypted</span>}
          </span>
          <span className="row-sub">
            {some(services.map((s) => s.label))}
            {owners.length > 0 && ` · ${some(owners)}`}
          </span>
          {p.showMachines && <Machines {...p} ids={machines} direction={first.direction} />}
        </div>
        <div className="row-side">
          <Amounts sent={sum((x) => x.sent)} received={sum((x) => x.received)} />
          <span className="when">
            {times(sum((x) => x.conns))} · {ago(Math.max(...p.peers.map((x) => x.lastSeen)), p.now)}
          </span>
        </div>
      </div>
      {open && (
        <ul className="rows group-members">
          {p.peers.map((peer) => (
            <PeerRow key={peer.direction + peer.name} {...p} peer={peer} />
          ))}
        </ul>
      )}
    </li>
  )
}

function PeerRow(p: ListProps & { peer: Peer }) {
  const { peer } = p
  const other = p.names.get(peer.peerId)
  const services = peer.services.map(service)
  const local = peer.local
  // A device's name stands in for a bare address.
  const device = !other && peer.name === peer.ip ? deviceLabel(p.devices, peer.ip) : ''
  return (
    <li>
      <div className="row-main">
        <span className="row-title">
          <a href={formatHash({ ...p.view, peer: peer.name, dir: peer.direction })}>{other ? other.name : device || peer.name}</a>
          {peer.new && (
            <span className="tag new" title="First seen in this time range">
              New
            </span>
          )}
          {other && <span className="tag">{YOURS_LABEL[other.kind]}</span>}
          <ScopeTag local={local} incoming={peer.direction === 'in'} />
          {services.some((s) => s.plain) && <span className="tag warn">Not encrypted</span>}
        </span>
        <span className="row-sub">
          {some(services.map((s) => s.label))}
          {other || device || peer.name !== peer.ip ? ` · ${peer.ip}` : ''}
          {/* Whose it is stands in for a name when all there is is an address. */}
          {peer.name === peer.ip && peer.owner && ` · ${peer.owner.name}`}
          {peer.ipCount > 1 && ` and ${peer.ipCount - 1} more`}
        </span>
        {p.showMachines && <Machines {...p} ids={peer.machines} direction={peer.direction} />}
      </div>
      <div className="row-side">
        <Amounts sent={peer.sent} received={peer.received} />
        <span className="when">
          {times(peer.conns)} · {ago(peer.lastSeen, p.now)}
        </span>
      </div>
    </li>
  )
}
