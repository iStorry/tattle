const UNITS = ['B', 'KB', 'MB', 'GB', 'TB']

export function bytes(n: number): string {
  if (n < 1) return '0 B'
  const i = Math.min(UNITS.length - 1, Math.floor(Math.log(n) / Math.log(1024)))
  const v = n / 1024 ** i
  return `${v >= 10 || i === 0 ? Math.round(v) : v.toFixed(1)} ${UNITS[i]}`
}

export function count(n: number): string {
  return n.toLocaleString()
}

export function plural(n: number, one: string, many = `${one}s`): string {
  return `${count(n)} ${n === 1 ? one : many}`
}

/** How long ago something happened, in everyday words. */
export function ago(then: number, now: number): string {
  if (!then) return 'never'
  const s = Math.max(0, Math.round((now - then) / 1000))
  if (s < 10) return 'just now'
  if (s < 60) return `${s} sec ago`
  const m = Math.round(s / 60)
  if (m < 60) return `${m} min ago`
  const h = Math.round(m / 60)
  if (h < 24) return `${h} hr ago`
  return `${Math.round(h / 24)} d ago`
}

export function clock(unixSeconds: number, withDay: boolean): string {
  const d = new Date(unixSeconds * 1000)
  const time = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  return withDay ? `${d.toLocaleDateString([], { weekday: 'short' })} ${time}` : time
}

type Service = { label: string; plain?: boolean }

// What a port is normally used for, in words a non-specialist would use.
// "plain" marks services that send their contents unencrypted.
const SERVICES: Record<string, Service> = {
  '20/tcp': { label: 'File transfer (FTP)', plain: true },
  '21/tcp': { label: 'File transfer (FTP)', plain: true },
  '22/tcp': { label: 'Remote login (SSH)' },
  '23/tcp': { label: 'Remote login (Telnet)', plain: true },
  '25/tcp': { label: 'Sending email' },
  '53/udp': { label: 'Name lookup (DNS)' },
  '53/tcp': { label: 'Name lookup (DNS)' },
  '67/udp': { label: 'Getting a network address' },
  '68/udp': { label: 'Getting a network address' },
  '80/tcp': { label: 'Web (HTTP)', plain: true },
  '123/udp': { label: 'Clock sync' },
  '143/tcp': { label: 'Reading email' },
  '443/tcp': { label: 'Secure web (HTTPS)' },
  '443/udp': { label: 'Secure web (HTTP/3)' },
  '445/tcp': { label: 'Windows file sharing' },
  '465/tcp': { label: 'Sending email' },
  '587/tcp': { label: 'Sending email' },
  '853/tcp': { label: 'Private name lookup' },
  '993/tcp': { label: 'Reading email' },
  '1194/udp': { label: 'VPN (OpenVPN)' },
  '1883/tcp': { label: 'Smart-home messages (MQTT)', plain: true },
  '2049/tcp': { label: 'File sharing (NFS)' },
  '3306/tcp': { label: 'Database (MySQL)' },
  '3389/tcp': { label: 'Remote desktop' },
  '5353/udp': { label: 'Local device discovery' },
  '5432/tcp': { label: 'Database (PostgreSQL)' },
  '6379/tcp': { label: 'Database (Redis)' },
  '8006/tcp': { label: 'Proxmox web panel' },
  '8009/tcp': { label: 'Casting to a TV' },
  '8096/tcp': { label: 'Jellyfin' },
  '8123/tcp': { label: 'Home Assistant' },
  '8883/tcp': { label: 'Smart-home messages (MQTT)' },
  '27017/tcp': { label: 'Database (MongoDB)' },
  '32400/tcp': { label: 'Plex' },
  '51820/udp': { label: 'VPN (WireGuard)' },
}

/** Describes a "443/tcp" style service key. */
export function service(key: string): Service {
  const known = SERVICES[key]
  if (known) return known
  const [port, proto] = key.split('/')
  if (proto === 'icmp') return { label: 'Ping' }
  return { label: `Port ${port} (${proto.toUpperCase()})` }
}

export const KIND_LABEL = { vm: 'VM', ct: 'Container', host: 'Host' } as const

/** The tag on a destination that is one of the owner's own machines. */
export const YOURS_LABEL = { vm: 'Your VM', ct: 'Your container', host: 'Your host' } as const
