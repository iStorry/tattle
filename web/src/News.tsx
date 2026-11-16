import { deviceLabel, type Devices, type Fresh, type Machine } from './api'
import { ago, plural, service } from './format'
import { ScopeTag } from './parts'
import { formatHash, type View } from './url'

/** Lists what machines did for the first time in the period being shown. */
export function WhatsNew(p: {
  fresh: Fresh
  names: Map<string, Machine>
  devices: Devices
  now: number
  view: View
  rangePhrase: string
  expanded: boolean
  onExpand: (expanded: boolean) => void
}) {
  const { fresh, names, now, view } = p
  const machineLink = (id: string) => <a href={formatHash({ ...view, machine: id })}>{names.get(id)?.name ?? id}</a>

  return (
    <section className="card news">
      <h2>New in the last {p.rangePhrase}</h2>
      {fresh.firsts.length === 0 ? (
        <p className="quiet">Nothing new. Every place contacted, and every caller, has been seen before.</p>
      ) : (
        <ul className="rows">
          {fresh.firsts.map((f) => {
            const incoming = f.direction === 'in'
            const svc = service(`${f.port}/${f.proto}`)
            const place = (
              <a href={formatHash({ ...view, machine: f.machine, peer: f.name, dir: f.direction })}>
                {names.get(f.peerId)?.name ?? (deviceLabel(p.devices, f.name) || f.name)}
              </a>
            )
            return (
              <li key={[f.machine, f.direction, f.name].join('|')}>
                <div className="row-main">
                  <span className="row-title">
                    {incoming ? (
                      <>
                        {place} <span className="said">connected to</span> {machineLink(f.machine)}
                      </>
                    ) : (
                      <>
                        {machineLink(f.machine)} <span className="said">contacted</span> {place}
                      </>
                    )}{' '}
                    <span className="said">for the first time</span>
                    <ScopeTag local={f.local} incoming={incoming} />
                    {svc.plain && <span className="tag warn">Not encrypted</span>}
                  </span>
                  <span className="row-sub">
                    {svc.label}
                    {(f.name !== f.ip || deviceLabel(p.devices, f.name)) && ` · ${f.ip}`}
                  </span>
                </div>
                <div className="row-side">
                  <span className="when">{ago(f.firstSeen, now)}</span>
                </div>
              </li>
            )
          })}
        </ul>
      )}
      {(fresh.more > 0 || p.expanded) && fresh.firsts.length > 0 && (
        <button className="toggle" onClick={() => p.onExpand(!p.expanded)}>
          {p.expanded ? 'Show fewer' : `Show ${plural(fresh.more, 'more', 'more')}`}
        </button>
      )}
      {fresh.learning.length > 0 && (
        <p className="learning">
          Still learning what is normal for{' '}
          {fresh.learning.map((l, i) => (
            <span key={l.machine}>
              {i > 0 && ', '}
              {names.get(l.machine)?.name ?? l.machine}
            </span>
          ))}
          . Nothing {fresh.learning.length === 1 ? 'it does' : 'they do'} is called new until a day after{' '}
          {fresh.learning.length === 1 ? 'it was' : 'each was'} first seen (the last one finishes{' '}
          {new Date(Math.max(...fresh.learning.map((l) => l.until))).toLocaleString([], {
            weekday: 'short',
            hour: '2-digit',
            minute: '2-digit',
          })}
          ).
        </p>
      )}
    </section>
  )
}
