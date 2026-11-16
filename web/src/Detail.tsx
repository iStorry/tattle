import { useEffect, useState } from 'react'
import { api, type Conversation, type Machine, type PeerDetail } from './api'
import { TrafficChart } from './Chart'
import { ago, bytes, count, KIND_LABEL, plural, service, YOURS_LABEL } from './format'
import { Amounts, Empty, ScopeTag, Tile } from './parts'
import { formatHash, RANGES, type View } from './url'

/** Everything about one destination or caller: who talks to it, how, and how much. */
export function PeerPage(p: { view: View; names: Map<string, Machine>; now: number }) {
  const { view, names, now } = p
  const { peer, dir, machine, range } = view
  const [detail, setDetail] = useState<PeerDetail | null>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let current = true
    const load = () => {
      if (document.hidden) return
      api
        .peer(range, dir, peer, machine)
        .then((d) => {
          if (!current) return
          setDetail(d)
          setFailed(false)
        })
        .catch(() => current && setFailed(true))
    }
    load()
    const id = setInterval(load, 3000)
    document.addEventListener('visibilitychange', load)
    return () => {
      current = false
      clearInterval(id)
      document.removeEventListener('visibilitychange', load)
    }
  }, [range, dir, peer, machine])

  const selected = machine ? names.get(machine) : undefined
  const back = (
    <a href={formatHash({ ...view, peer: '' })}>← Back to {selected ? selected.name : 'all machines'}</a>
  )
  // What is on screen may still be the previous destination's, for a moment.
  const fresh = detail && detail.name === peer && detail.direction === dir ? detail : null
  if (!fresh) {
    return (
      <main>
        <div className="heading">
          {back}
          <h1>{peer}</h1>
          <p>{failed ? 'Could not load this. Retrying…' : 'Loading…'}</p>
        </div>
      </main>
    )
  }

  const other = names.get(fresh.peerId)
  const incoming = dir === 'in'
  const rangePhrase = RANGES.find((r) => r.seconds === range)?.phrase ?? ''
  const services = [...new Set(fresh.conversations.map((c) => `${c.port}/${c.proto}`))].map(service)
  const byMachine = groupByMachine(fresh.conversations, incoming)
  // A destination known by name lists its addresses; one known only by its
  // address shows that address when the heading shows a machine name instead.
  const others = fresh.ips.filter((ip) => ip !== fresh.name)
  const addresses = others.length > 0 ? others : other ? [fresh.name] : []
  const quiet = fresh.conns === 0

  return (
    <main>
      <div className="heading">
        {back}
        <h1>
          {other ? other.name : fresh.name}
          {other && <span className="tag">{YOURS_LABEL[other.kind]}</span>}
          <ScopeTag local={fresh.local} incoming={incoming} />
          {services.some((s) => s.plain) && <span className="tag warn">Not encrypted</span>}
        </h1>
        <p>
          {incoming ? 'Connected to ' : 'Contacted by '}
          {selected ? selected.name : 'your machines'} in the last {rangePhrase}
          {selected && (
            <>
              {' · '}
              <a href={formatHash({ ...view, machine: '' })}>show all machines</a>
            </>
          )}
        </p>
        <dl className="facts">
          {addresses.length > 0 && (
            <div>
              <dt>{addresses.length === 1 ? 'Address' : 'Addresses'}</dt>
              <dd>
                {addresses.slice(0, 6).join(', ')}
                {addresses.length > 6 && ` and ${addresses.length - 6} more`}
              </dd>
            </div>
          )}
          <div>
            <dt>First seen</dt>
            <dd>{fresh.firstSeen ? when(fresh.firstSeen, now) : 'Never'}</dd>
          </div>
          <div>
            <dt>Last seen</dt>
            <dd>{fresh.lastSeen ? when(fresh.lastSeen, now) : `Not in the last ${rangePhrase}`}</dd>
          </div>
        </dl>
      </div>

      {quiet ? (
        <section className="card">
          <Empty>
            {fresh.firstSeen
              ? `Nothing in the last ${rangePhrase}. Choose a longer time range to see earlier traffic.`
              : 'Tattle has no record of this. It may be older than the history that is kept.'}
          </Empty>
        </section>
      ) : (
        <>
          <section className="tiles">
            <Tile label="Sent to it" value={bytes(fresh.sent)} />
            <Tile label="Received from it" value={bytes(fresh.received)} />
            <Tile label="Connections" value={count(fresh.conns)} />
            <Tile label={incoming ? 'Machines reached' : 'Machines using it'} value={count(byMachine.length)} />
          </section>

          <section className="card">
            <h2>Traffic over time</h2>
            <TrafficChart data={fresh.timeline} range={range} />
          </section>

          <section className="card">
            <h2>{incoming ? 'Which machines it connected to' : 'Which machines use it'}</h2>
            <ul className="rows">
              {byMachine.map((m) => {
                const machineInfo = names.get(m.id)
                return (
                  <li key={m.id}>
                    <div className="row-main">
                      <span className="row-title">
                        <a href={formatHash({ ...view, peer: '', machine: m.id })}>{machineInfo?.name ?? m.id}</a>
                        {machineInfo && <span className="tag">{KIND_LABEL[machineInfo.kind]}</span>}
                      </span>
                      <span className="row-sub">{[...m.services].map((k) => service(k).label).join(', ')}</span>
                    </div>
                    <div className="row-side">
                      <Amounts sent={m.sent} received={m.received} />
                      <span className="when">
                        {plural(m.conns, 'time')} · {ago(m.lastSeen, now)}
                      </span>
                    </div>
                  </li>
                )
              })}
            </ul>
          </section>

          <section className="card">
            <h2>Every connection</h2>
            <p className="explain">One line for each machine, address and service.</p>
            <div className="table-wrap">
              <table className="table">
                <thead>
                  <tr>
                    <th>Machine</th>
                    <th>{incoming ? 'From' : 'To'}</th>
                    <th>Service</th>
                    <th className="num">Sent</th>
                    <th className="num">Received</th>
                    <th className="num">Times</th>
                    <th>Last seen</th>
                  </tr>
                </thead>
                <tbody>
                  {fresh.conversations.map((c) => {
                    const mine = incoming ? c.serverId : c.clientId
                    const svc = service(`${c.port}/${c.proto}`)
                    return (
                      <tr key={[c.clientIp, c.serverIp, c.port, c.proto].join('|')}>
                        <td>{names.get(mine)?.name ?? mine}</td>
                        <td className="mono">{incoming ? c.clientIp : c.serverIp}</td>
                        <td>
                          {svc.label}
                          <span className="port">
                            {' '}
                            port {c.port}/{c.proto}
                          </span>
                        </td>
                        <td className="num">{bytes(c.sent)}</td>
                        <td className="num">{bytes(c.received)}</td>
                        <td className="num">{count(c.conns)}</td>
                        <td>{ago(c.lastSeen, now)}</td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          </section>
        </>
      )}
    </main>
  )
}

/** "3 min ago", with the date and time for anything older than a day. */
function when(ms: number, now: number): string {
  if (now - ms < 24 * 60 * 60 * 1000) return ago(ms, now)
  return new Date(ms).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })
}

type MachineUse = { id: string; services: Set<string>; sent: number; received: number; conns: number; lastSeen: number }

/** Totals each machine's dealings with the peer, busiest first. */
function groupByMachine(rows: Conversation[], incoming: boolean): MachineUse[] {
  const byId = new Map<string, MachineUse>()
  for (const c of rows) {
    const id = incoming ? c.serverId : c.clientId
    let m = byId.get(id)
    if (!m) {
      m = { id, services: new Set(), sent: 0, received: 0, conns: 0, lastSeen: 0 }
      byId.set(id, m)
    }
    m.services.add(`${c.port}/${c.proto}`)
    m.sent += c.sent
    m.received += c.received
    m.conns += c.conns
    m.lastSeen = Math.max(m.lastSeen, c.lastSeen)
  }
  return [...byId.values()].sort((a, b) => b.sent + b.received - (a.sent + a.received))
}
