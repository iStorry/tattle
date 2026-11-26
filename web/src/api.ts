export type Status = {
  version: string
  demo: boolean
  interfaces: string[]
  retentionDays: number
  detailDays: number
  localNetworks: string[] | null
  started: number
  now: number
  packets: number
  dropped: number
  ownerRanges: number
  health: HealthState
}

export type Scope = 'all' | 'internet' | 'local'

export type Settings = {
  localNets: string[]
  privateIsLocal: boolean
  show: Scope
  retentionDays: number // days of history kept in all
  detailDays: number // how many of those keep minute-by-minute detail
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
  new: boolean
  owner?: Owner
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

export type HealthState = 'ok' | 'off' | 'warn' | 'bad'

export type Health = {
  state: HealthState // the worst of the checks
  version: string
  uptime: string
  checks: {
    id: string
    title: string
    state: HealthState
    summary: string
    advice: string // what to do about it, or ''
    facts: { label: string; value: string }[]
  }[]
}

/** The network an internet address belongs to. */
export type Owner = {
  name: string
  asn: number
  country: string // where the network is registered, as a two-letter code, or ''
}

const regionNames = typeof Intl.DisplayNames === 'function' ? new Intl.DisplayNames(undefined, { type: 'region' }) : null

/** "United States" for "US"; the code itself if the browser cannot say. */
export function countryName(code: string): string {
  if (!code) return ''
  try {
    return regionNames?.of(code) ?? code
  } catch {
    return code
  }
}

/** A name for something on the network that is not a VM or container. */
export type Device = {
  ip: string
  name: string // chosen by the owner, or ''
  announced: string // what the device calls itself, or ''
}

export type Devices = Map<string, Device>

/** Something on the network map: a machine, the router, or another device. */
export type MapNode = {
  id: string // a machine ID, or "dev-" and the hardware address
  kind: 'host' | 'vm' | 'ct' | 'router' | 'device'
  name: string // a machine's name; for a device, the owner's name or ''
  announced: string // what a device calls itself, or ''
  mac: string
  ips: string[] // the one to show comes first
  bridges: string[]
  firstSeen: number
  lastSeen: number // 0 if not heard from in the range
}

/** Traffic between two nodes; "internet" stands for everything outside. */
export type MapLink = { a: string; b: string; bytes: number }

export type NetworkMap = { nodes: MapNode[]; links: MapLink[] }

/** The name to show for an address: the owner's, else the device's own, else none. */
export function deviceLabel(devices: Devices, ip: string): string {
  const d = devices.get(ip)
  return d ? d.name || d.announced : ''
}

/** Whether a destination is known only by its address, not by a domain name. */
export function isAddress(name: string): boolean {
  return name.includes(':') || /^\d{1,3}(\.\d{1,3}){3}$/.test(name)
}

export type First = {
  machine: string
  direction: 'out' | 'in'
  name: string
  firstSeen: number
  ip: string
  peerId: string
  port: number
  proto: string
  local: boolean
  owner?: Owner
}

export type Fresh = {
  firsts: First[]
  more: number
  learning: { machine: string; until: number }[]
}

export type PeerDetail = {
  name: string
  direction: 'out' | 'in'
  peerId: string
  local: boolean
  owner?: Owner
  ips: string[]
  firstSeen: number // earliest in the kept history, or 0
  lastSeen: number // within the time range, or 0
  sent: number
  received: number
  conns: number
  timeline: Timeline
  conversations: Conversation[]
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

export type AuthState = {
  enabled: boolean // false in demo mode, where there is nothing to sign in to
  setup: boolean // no password yet: show first-time setup
  signedIn: boolean
}

let unauthorized: (() => void) | null = null

/** Registers what to do when the server says the session has ended. */
export function onUnauthorized(handler: (() => void) | null) {
  unauthorized = handler
}

async function get<T>(path: string, params: Record<string, string | number> = {}): Promise<T> {
  const query = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== '') query.set(k, String(v))
  }
  const res = await fetch(`${path}?${query}`)
  if (res.status === 401) unauthorized?.()
  if (!res.ok) throw new Error(`${path}: ${res.status}`)
  return res.json() as Promise<T>
}

/** Sends JSON. The server explains a refusal in plain words, which becomes the error. */
async function send(method: string, path: string, body: unknown): Promise<Response> {
  const res = await fetch(path, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    const text = (await res.text()).trim()
    if (res.status === 401 && !path.startsWith('/api/auth/')) unauthorized?.()
    throw new Error(text ? text.charAt(0).toUpperCase() + text.slice(1) + '.' : 'Something went wrong.')
  }
  return res
}

export const api = {
  status: () => get<Status>('/api/status'),
  machines: (range: number, scope: Scope) => get<Machine[]>('/api/machines', { range, scope }),
  timeline: (range: number, machine: string, scope: Scope) =>
    get<Timeline>('/api/timeline', { range, machine, scope }),
  peers: (range: number, machine: string, scope: Scope) => get<Peer[]>('/api/peers', { range, machine, scope }),
  peer: (range: number, direction: 'out' | 'in', name: string, machine: string) =>
    get<PeerDetail>('/api/peer', { range, direction, name, machine }),
  split: (range: number, machine: string) => get<Split>('/api/split', { range, machine }),
  health: () => get<Health>('/api/health'),
  devices: () => get<Device[]>('/api/devices'),
  map: (range: number) => get<NetworkMap>('/api/map', { range }),
  nameDevice: async (ip: string, name: string): Promise<Device[]> =>
    (await send('PUT', '/api/devices', { ip, name })).json() as Promise<Device[]>,
  fresh: (range: number, machine: string, scope: Scope, limit: number) =>
    get<Fresh>('/api/new', { range, machine, scope, limit }),
  settings: () => get<SettingsInfo>('/api/settings'),
  saveSettings: async (settings: Settings): Promise<SettingsInfo> =>
    (await send('PUT', '/api/settings', settings)).json() as Promise<SettingsInfo>,
  auth: () => get<AuthState>('/api/auth'),
  setup: (code: string, password: string) => send('POST', '/api/auth/setup', { code, password }),
  login: (password: string) => send('POST', '/api/auth/login', { password }),
  logout: () => send('POST', '/api/auth/logout', {}),
  changePassword: (current: string, next: string) => send('POST', '/api/auth/password', { current, new: next }),
}
