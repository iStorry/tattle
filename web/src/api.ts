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
  retentionDays: number
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

export type PeerDetail = {
  name: string
  direction: 'out' | 'in'
  peerId: string
  local: boolean
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
  settings: () => get<SettingsInfo>('/api/settings'),
  saveSettings: async (settings: Settings): Promise<SettingsInfo> =>
    (await send('PUT', '/api/settings', settings)).json() as Promise<SettingsInfo>,
  auth: () => get<AuthState>('/api/auth'),
  setup: (code: string, password: string) => send('POST', '/api/auth/setup', { code, password }),
  login: (password: string) => send('POST', '/api/auth/login', { password }),
  logout: () => send('POST', '/api/auth/logout', {}),
  changePassword: (current: string, next: string) => send('POST', '/api/auth/password', { current, new: next }),
}
