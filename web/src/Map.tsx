import { useEffect, useMemo, useState } from 'react'
import { api, type MapNode, type NetworkMap } from './api'
import { DeviceName } from './Detail'
import { Icon as Glyph } from './icons'
import { ago, bytes, KIND_LABEL, plural } from './format'
import { Empty, Tile } from './parts'
import { formatHash, type View } from './url'

const INTERNET = 'internet'
// A device that has said nothing for this long is drawn faded: it may have
// left, or may only be asleep.
const QUIET_MS = 15 * 60 * 1000

const WHAT = { ...KIND_LABEL, router: 'Router', device: 'Device' } as const

/** The name to show for a node: the owner's, the device's own, or its address. */
function label(n: MapNode): string {
  return n.name || n.announced || (n.kind === 'router' ? 'Router' : n.ips[0] || n.mac)
}

function Icon({ kind }: { kind: MapNode['kind'] | typeof INTERNET }) {
  return <Glyph name={kind} className="map-icon" />
}

/** Everything connected to the network, drawn the way it is wired together. */
export function MapPage(p: { view: View; now: number; rangePhrase: string; onPick: (node: string) => void }) {
  const { range, node: picked } = p.view
  const [map, setMap] = useState<NetworkMap | null>(null)
  const [failed, setFailed] = useState(false)
  const [reload, setReload] = useState(0)

  useEffect(() => {
    let current = true
    const load = () => {
      if (document.hidden) return
      api
        .map(range)
        .then((m) => {
          if (!current) return
          setMap(m)
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
  }, [range, reload])

  // How much each node exchanged with the one that is picked.
  const withPicked = useMemo(() => {
    const out = new Map<string, number>()
    if (!map || !picked) return out
    for (const l of map.links) {
      if (l.a === picked) out.set(l.b, l.bytes)
      else if (l.b === picked) out.set(l.a, l.bytes)
    }
    return out
  }, [map, picked])

  if (!map) {
    return (
      <main className="page netmap">
        <Empty>{failed ? 'The map could not be loaded.' : 'Loading…'}</Empty>
      </main>
    )
  }

  const byId = new Map(map.nodes.map((n) => [n.id, n]))
  const router = map.nodes.find((n) => n.kind === 'router')
  const isMachine = (n: MapNode) => n.kind === 'host' || n.kind === 'vm' || n.kind === 'ct'
  const machines = map.nodes.filter(isMachine)
  const devices = map.nodes.filter((n) => !isMachine(n))
  const quiet = (n: MapNode) => (isMachine(n) ? n.lastSeen === 0 : p.now - n.lastSeen > QUIET_MS)
  const internetTotal = map.links.filter((l) => l.a === INTERNET || l.b === INTERNET).reduce((a, l) => a + l.bytes, 0)

  // The router's bridge is the way out, so it is drawn first.
  const bridges = [...new Set(map.nodes.flatMap((n) => n.bridges))].sort(
    (a, b) => Number(router?.bridges.includes(b) ?? false) - Number(router?.bridges.includes(a) ?? false) || a.localeCompare(b),
  )
  const elsewhere = map.nodes.filter((n) => n.bridges.length === 0)

  const pick = (id: string) => p.onPick(id === picked ? '' : id)
  const card = (n: MapNode) => {
    const shared = withPicked.get(n.id)
    return (
      <button
        key={n.id}
        className="map-node"
        data-quiet={quiet(n) || undefined}
        data-dim={(picked !== '' && picked !== n.id && shared === undefined) || undefined}
        data-linked={shared !== undefined || undefined}
        aria-pressed={picked === n.id}
        onClick={() => pick(n.id)}
      >
        <Icon kind={n.kind} />
        <span className="map-node-text">
          <span className="map-node-name">{label(n)}</span>
          <span className="map-node-meta">
            {shared !== undefined
              ? `${bytes(shared)} exchanged`
              : [n.name || n.announced || n.kind === 'router' ? n.ips[0] : '', n.lastSeen ? ago(n.lastSeen, p.now) : isMachine(n) ? 'No traffic' : '']
                  .filter(Boolean)
                  .join(' · ')}
          </span>
        </span>
        {isMachine(n) && <span className="kind">{WHAT[n.kind]}</span>}
      </button>
    )
  }
  const group = (title: string, nodes: MapNode[]) =>
    nodes.length > 0 && (
      <div className="map-group">
        <h3>
          {title} <span>{nodes.length}</span>
        </h3>
        <div className="map-grid">{nodes.map(card)}</div>
      </div>
    )

  const selected = picked === INTERNET ? null : byId.get(picked)
  const since = p.now - range * 1000

  return (
    <main className="page netmap">
      <div className="heading">
        <h1>Network map</h1>
        <p>
          Everything heard on your network in the last {p.rangePhrase}. Tattle only listens, so a device appears once it
          has announced itself, which most do within minutes.
        </p>
      </div>

      <section className="tiles">
        <Tile label="Devices" value={String(devices.length)} hint="Things on the network that are not VMs or containers" />
        <Tile label="Machines on this host" value={String(machines.length)} />
        <Tile
          label="Around now"
          value={String(devices.filter((n) => !quiet(n)).length)}
          hint="Devices heard from in the last 15 minutes"
        />
        <Tile
          label="New"
          value={String(devices.filter((n) => n.firstSeen >= since).length)}
          hint={`Devices first heard in the last ${p.rangePhrase}`}
        />
      </section>

      <div className="map-layout">
        <section className="card map-tree" aria-label="Network map">
          <button
            className="map-node map-trunk"
            aria-pressed={picked === INTERNET}
            data-linked={withPicked.has(INTERNET) || undefined}
            onClick={() => pick(INTERNET)}
          >
            <Icon kind={INTERNET} />
            <span className="map-node-text">
              <span className="map-node-name">Internet</span>
              <span className="map-node-meta">
                {withPicked.has(INTERNET)
                  ? `${bytes(withPicked.get(INTERNET) ?? 0)} exchanged`
                  : `${bytes(internetTotal)} in the last ${p.rangePhrase}`}
              </span>
            </span>
          </button>
          <i className="map-wire" />
          {router && (
            <>
              <div className="map-trunk-slot">{card(router)}</div>
              <i className="map-wire" />
            </>
          )}

          {bridges.length === 0 && elsewhere.length === 0 && <Empty>Nothing has been heard on the network yet.</Empty>}
          {bridges.map((b) => {
            const here = map.nodes.filter((n) => n.bridges.includes(b) && n !== router)
            return (
              <div className="map-bridge" key={b}>
                <div className="map-bridge-head">
                  <strong>{b}</strong>
                  <span>Bridge · {plural(here.length + (router?.bridges.includes(b) ? 1 : 0), 'thing')} attached</span>
                </div>
                <div className="map-branches">
                  {group('On this host', here.filter(isMachine))}
                  {group(
                    'Other devices',
                    // Named ones first, then the rest in address order.
                    here
                      .filter((n) => !isMachine(n))
                      .sort(
                        (a, b) =>
                          Number(!(a.name || a.announced)) - Number(!(b.name || b.announced)) ||
                          label(a).localeCompare(label(b), undefined, { numeric: true }),
                      ),
                  )}
                </div>
              </div>
            )
          })}
          {elsewhere.length > 0 && (
            <div className="map-bridge">
              <div className="map-bridge-head">
                <strong>Elsewhere</strong>
                <span>Not attached to a bridge Tattle is watching</span>
              </div>
              <div className="map-branches">{group('Machines', elsewhere)}</div>
            </div>
          )}
        </section>

        <aside className="card map-detail" aria-live="polite">
          {picked === INTERNET ? (
            <>
              <h2>Internet</h2>
              <p className="map-detail-title">Everything outside your network</p>
              <Talked map={map} byId={byId} picked={picked} rangePhrase={p.rangePhrase} onPick={p.onPick} />
            </>
          ) : selected ? (
            <>
              <h2>{WHAT[selected.kind]}</h2>
              <p className="map-detail-title">{label(selected)}</p>
              {!isMachine(selected) && selected.ips[0] && (
                <DeviceName
                  key={selected.id}
                  ip={selected.ips[0]}
                  device={{ ip: selected.ips[0], name: selected.name, announced: selected.announced }}
                  onDevices={() => setReload((n) => n + 1)}
                />
              )}
              <dl className="facts map-facts">
                <div>
                  <dt>{selected.ips.length === 1 ? 'Address' : 'Addresses'}</dt>
                  <dd>
                    {selected.ips.slice(0, 3).join(', ') || 'Not known yet'}
                    {selected.ips.length > 3 && ` and ${selected.ips.length - 3} more`}
                  </dd>
                </div>
                {selected.mac && (
                  <div>
                    <dt>Hardware address</dt>
                    <dd className="mono">{selected.mac}</dd>
                  </div>
                )}
                <div>
                  <dt>Attached to</dt>
                  <dd>{selected.bridges.join(', ') || 'No watched bridge'}</dd>
                </div>
                {selected.firstSeen > 0 && (
                  <div>
                    <dt>First heard</dt>
                    <dd>{new Date(selected.firstSeen).toLocaleDateString([], { year: 'numeric', month: 'short', day: 'numeric' })}</dd>
                  </div>
                )}
                <div>
                  <dt>{isMachine(selected) ? 'Last traffic' : 'Last heard'}</dt>
                  <dd>{selected.lastSeen ? ago(selected.lastSeen, p.now) : `Not in the last ${p.rangePhrase}`}</dd>
                </div>
              </dl>
              <Talked map={map} byId={byId} picked={picked} rangePhrase={p.rangePhrase} onPick={p.onPick} />
              {isMachine(selected) && (
                <a className="button" href={formatHash({ ...p.view, page: 'dashboard', machine: selected.id, peer: '' })}>
                  See what it contacted
                </a>
              )}
            </>
          ) : (
            <>
              <h2>Pick something</h2>
              <p className="help">
                Choose anything on the map to see its addresses, when it was last heard, and which of your machines it
                talked to. Faded entries have been quiet for a while.
              </p>
              <p className="help">
                Tattle sees who is on the network, and traffic to or from your VMs, containers and the host. It does not
                see what two other devices say to each other.
              </p>
            </>
          )}
        </aside>
      </div>
    </main>
  )
}

/** Who the picked node exchanged traffic with, busiest first. */
function Talked(p: {
  map: NetworkMap
  byId: Map<string, MapNode>
  picked: string
  rangePhrase: string
  onPick: (node: string) => void
}) {
  const rows = p.map.links
    .filter((l) => l.a === p.picked || l.b === p.picked)
    .map((l) => ({ id: l.a === p.picked ? l.b : l.a, bytes: l.bytes }))
  if (rows.length === 0) {
    return <p className="help">No traffic with your machines in the last {p.rangePhrase}.</p>
  }
  return (
    <>
      <h3 className="map-talked-title">Talked to in the last {p.rangePhrase}</h3>
      <ul className="map-talked">
        {rows.map((r) => {
          const n = p.byId.get(r.id)
          return (
            <li key={r.id}>
              <button className="toggle" onClick={() => p.onPick(r.id)}>
                {r.id === INTERNET ? 'Internet' : n ? label(n) : r.id}
              </button>
              <span>{bytes(r.bytes)}</span>
            </li>
          )
        })}
      </ul>
    </>
  )
}
