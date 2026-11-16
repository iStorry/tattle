import type { ReactNode } from 'react'
import { bytes } from './format'

// Small pieces shared by the dashboard, the detail page and the sign-in pages.

export function Logo() {
  return (
    <span className="logo">
      <svg viewBox="0 0 32 32" aria-hidden="true">
        <rect width="32" height="32" rx="8" />
        <path d="M8 11h16M16 11v12" />
        <circle cx="24" cy="22" r="2.6" />
      </svg>
      Tattle
    </span>
  )
}

export function Tile({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="tile" title={hint}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  )
}

/** How much a machine sent and received. */
export function Amounts({ sent, received }: { sent: number; received: number }) {
  return (
    <span className="amounts">
      <span title="Sent by your machine">↑ {bytes(sent)}</span>
      <span title="Received by your machine">↓ {bytes(received)}</span>
    </span>
  )
}

/** Says whether the other end is inside the home network or on the internet. */
export function ScopeTag({ local, incoming }: { local: boolean; incoming?: boolean }) {
  if (local) return <span className="tag local">Inside your network</span>
  return <span className={incoming ? 'tag warn' : 'tag internet'}>{incoming ? 'From the internet' : 'Internet'}</span>
}

export function Empty({ children }: { children: ReactNode }) {
  return <p className="empty">{children}</p>
}
