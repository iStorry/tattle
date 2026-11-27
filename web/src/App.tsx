import { useEffect, useMemo, useState } from 'react'
import { api, deviceLabel, type Device, type Devices, type Scope, type SettingsInfo, type Status } from './api'
import { Dashboard, type Data } from './Dashboard'
import { PeerPage } from './Detail'
import { HealthPage } from './Health'
import { MachinesPage } from './Machines'
import { MapPage } from './Map'
import { Logo } from './parts'
import { usePolling } from './poll'
import { Rail, TopBar } from './Shell'
import { SettingsPage } from './Settings'
import { useTheme } from './theme'
import { formatHash, RANGES, useView } from './url'

function byAddress(list: Device[]): Devices {
  return new Map(list.map((d) => [d.ip, d]))
}

const TITLES = { dashboard: 'Dashboard', map: 'Topology', machines: 'Machines', health: 'Health', settings: 'Settings' } as const

export default function App({ canSignOut, onSignOut }: { canSignOut: boolean; onSignOut: () => void }) {
  // What is being looked at comes from the address bar, so it can be
  // bookmarked, shared and stepped through with the back button.
  const [view, go] = useView()
  const { range, machine } = view
  const route = view.page
  const [theme, toggleTheme] = useTheme()

  const [info, setInfo] = useState<SettingsInfo | null>(null)
  // Unless the address says otherwise, the saved settings decide what the
  // dashboard opens with, and they are not known until they have loaded.
  const scope: Scope | null = info ? (view.scope ?? info.settings.show) : null

  // "What's new" shows a handful until asked for more.
  const [moreNew, setMoreNew] = useState(false)
  const newLimit = moreNew ? 100 : 5

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

  // Which question the figures on screen answer. Until it matches what is
  // being asked for, they belong to the previous range or machine.
  const asking = `${range}|${machine}|${scope}`
  const [answered, setAnswered] = useState('')

  usePolling(
    async (wanted) => {
      if (scope === null) return
      try {
        const [st, machines, timeline, peers, split, fresh, devices] = await Promise.all([
          api.status(),
          api.machines(range, scope),
          api.timeline(range, machine, scope),
          api.peers(range, machine, scope),
          api.split(range, machine),
          api.fresh(range, machine, scope, newLimit),
          api.devices(),
        ])
        if (!wanted()) return
        setStatus(st)
        setNow(st.now)
        setData({ machines, timeline, peers, split, fresh, devices: byAddress(devices) })
        setAnswered(asking)
        setFailed(false)
      } catch {
        if (wanted()) setFailed(true)
      }
    },
    [range, machine, scope, info, newLimit],
    // Health, Settings and the map keep themselves up to date; these
    // figures are only refreshed while a page that shows them is open.
    route === 'dashboard' || route === 'machines',
  )

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
  const peerName = peerMachine?.name || (data && deviceLabel(data.devices, view.peer)) || view.peer
  const pageTitle = route === 'dashboard' ? peerName || (selected?.name ?? '') : TITLES[route]
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

  const rangePhrase = RANGES.find((r) => r.seconds === range)?.phrase ?? ''
  const stale = answered !== asking
  const timed = route === 'dashboard' || route === 'map' || route === 'machines'
  const backHref = formatHash({ ...view, page: 'dashboard' })

  return (
    <div className="console">
      <Rail
        view={view}
        health={status.health}
        theme={theme}
        onTheme={toggleTheme}
        canSignOut={canSignOut}
        onSignOut={onSignOut}
      />
      <div className="console-main">
        <TopBar
          title={route === 'dashboard' && view.peer ? 'Destination' : TITLES[route]}
          status={status}
          failed={failed}
          range={timed ? range : undefined}
          onRange={(seconds) => go({ range: seconds })}
        />

        {status.demo && (
          <p className="banner">
            This is invented traffic from a pretend home lab. Run Tattle on your Proxmox host without{' '}
            <code>--demo</code> to see your own machines.
          </p>
        )}

        {route === 'map' ? (
          <MapPage view={view} now={now} rangePhrase={rangePhrase} onPick={(node) => go({ node })} />
        ) : route === 'health' ? (
          <HealthPage backHref={backHref} />
        ) : route === 'settings' ? (
          <SettingsPage
            info={info}
            canChangePassword={canSignOut}
            backHref={backHref}
            onSaved={(saved) => {
              setInfo(saved)
              // Follow the newly saved choice, not one left in the address.
              go({ scope: null }, true)
            }}
          />
        ) : route === 'machines' ? (
          <MachinesPage
            machines={data.machines}
            timeline={data.timeline}
            peers={data.peers}
            split={data.split}
            devices={data.devices}
            view={view}
            go={go}
            now={now}
            stale={stale}
            rangePhrase={rangePhrase}
          />
        ) : view.peer ? (
          <PeerPage
            view={view}
            names={names}
            devices={data.devices}
            onDevices={(list) => setData((d) => d && { ...d, devices: byAddress(list) })}
            now={now}
          />
        ) : (
          <Dashboard
            data={data}
            status={status}
            info={info}
            scope={scope}
            view={view}
            go={go}
            now={now}
            stale={stale}
            rangePhrase={rangePhrase}
            moreNew={moreNew}
            onMoreNew={setMoreNew}
            names={names}
            selected={selected}
          />
        )}
      </div>
    </div>
  )
}
