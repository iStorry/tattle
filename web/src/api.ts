export type Status = {
  version: string
  demo: boolean
  interfaces: string[]
  retentionDays: number
  localNetworks: string[] | null
  started: number
  now: number
  packets: number
  dropped: number
}

export type Scope = 'all' | 'internet' | 'local'

export type Settings = {
  localNets: string[]
  privateIsLocal: boolean
  show: Scope
}

export type SettingsInfo = { settings: Settings; detected: string[] }

export type Share = { bytes: number; places: number }
export type Split = { internet: Share; local: Share }

export type Machine = {
  id: string
  kind: 'vm' | 'ct' | 'host'
  vmid: number
  name: string
  node: string
  macs: string[]
  ips: string[]
  present: boolean
  sent: number
  received: number
  conns: number
  destinations: number
  lastSeen: number
  spark: number[]
}

export type Timeline = {
  step: number
  points: { t: number; sent: number; received: number }[]
}

export type Peer = {
  name: string
  direction: 'out' | 'in'
  peerId: string
  local: boolean
  ip: string
  ipCount: number
  machines: string[]
  services: string[]
  sent: number
  received: number
  conns: number
  firstSeen: number
  lastSeen: number
}

export type Conversation = {
  clientId: string
  clientIp: string
  serverId: string
  serverIp: string
  port: number
  proto: string
  domain: string
  local: boolean
  sent: number
  received: number
  conns: number
  firstSeen: number
  lastSeen: number
}

async function get<T>(path: string, params: Record<string, string | number> = {}): Promise<T> {
  const query = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== '') query.set(k, String(v))
  }
  const res = await fetch(`${path}?${query}`)
  if (!res.ok) throw new Error(`${path}: ${res.status}`)
  return res.json() as Promise<T>
}

export const api = {
  status: () => get<Status>('/api/status'),
  machines: (range: number, scope: Scope) => get<Machine[]>('/api/machines', { range, scope }),
  timeline: (range: number, machine: string, scope: Scope) =>
    get<Timeline>('/api/timeline', { range, machine, scope }),
  peers: (range: number, machine: string, scope: Scope) => get<Peer[]>('/api/peers', { range, machine, scope }),
  activity: (range: number, machine: string, scope: Scope, q: string) =>
    get<Conversation[]>('/api/activity', { range, machine, scope, q, limit: 300 }),
  split: (range: number, machine: string) => get<Split>('/api/split', { range, machine }),
  settings: () => get<SettingsInfo>('/api/settings'),
  saveSettings: async (settings: Settings): Promise<SettingsInfo> => {
    const res = await fetch('/api/settings', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(settings),
    })
    // The server explains a rejected setting in plain words.
    if (!res.ok) throw new Error((await res.text()).trim() || 'The settings could not be saved.')
    return res.json() as Promise<SettingsInfo>
  },
}
