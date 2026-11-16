import { useCallback, useEffect, useState } from 'react'
import type { Scope } from './api'

// What the dashboard is showing lives in the address bar, after the "#":
//
//   #/?machine=vm-100&range=24h&tab=in&show=internet&sort=data&q=docker
//   #/?peer=api.github.com&range=24h
//   #/settings
//
// so a view can be bookmarked or sent to someone, a reload keeps it, and
// the browser's back button steps through what was looked at.

export type Tab = 'out' | 'in'
export type Sort = 'recent' | 'data'

export const RANGES = [
  { key: '15m', label: '15 min', phrase: '15 minutes', seconds: 15 * 60 },
  { key: '1h', label: '1 hour', phrase: 'hour', seconds: 60 * 60 },
  { key: '24h', label: '24 hours', phrase: '24 hours', seconds: 24 * 60 * 60 },
  { key: '7d', label: '7 days', phrase: '7 days', seconds: 7 * 24 * 60 * 60 },
] as const

const DEFAULT_RANGE = RANGES[1]

export type View = {
  page: 'dashboard' | 'settings'
  machine: string // a machine ID, or '' for all machines
  range: number // seconds
  tab: Tab
  scope: Scope | null // null means "whatever the saved settings say"
  sort: Sort
  q: string
  peer: string // the destination or caller whose detail page is open, or ''
  dir: Tab // whether peer is somewhere machines contacted, or a caller
}

function oneOf<T extends string>(value: string | null, allowed: readonly T[], fallback: T): T {
  return allowed.includes(value as T) ? (value as T) : fallback
}

/** Reads a view from the part of the address after "#". Anything unrecognised falls back to the default. */
export function parseHash(hash: string): View {
  const [path, query = ''] = hash.replace(/^#/, '').split('?')
  const p = new URLSearchParams(query)
  const show = p.get('show')
  return {
    page: path === '/settings' ? 'settings' : 'dashboard',
    machine: p.get('machine') ?? '',
    range: (RANGES.find((r) => r.key === p.get('range')) ?? DEFAULT_RANGE).seconds,
    tab: oneOf(p.get('tab'), ['out', 'in'], 'out'),
    scope: show === null ? null : oneOf<Scope>(show, ['all', 'internet', 'local'], 'all'),
    sort: oneOf(p.get('sort'), ['recent', 'data'], 'recent'),
    q: p.get('q') ?? '',
    peer: p.get('peer') ?? '',
    dir: oneOf(p.get('dir'), ['out', 'in'], 'out'),
  }
}

/** Writes a view as an address, leaving out everything that is at its default. */
export function formatHash(v: View): string {
  const p = new URLSearchParams()
  if (v.machine) p.set('machine', v.machine)
  if (v.range !== DEFAULT_RANGE.seconds) p.set('range', RANGES.find((r) => r.seconds === v.range)?.key ?? DEFAULT_RANGE.key)
  if (v.tab !== 'out') p.set('tab', v.tab)
  if (v.scope !== null) p.set('show', v.scope)
  if (v.sort !== 'recent') p.set('sort', v.sort)
  if (v.q) p.set('q', v.q)
  if (v.peer) {
    p.set('peer', v.peer)
    if (v.dir !== 'out') p.set('dir', v.dir)
  }
  const query = p.toString()
  return `#${v.page === 'settings' ? '/settings' : '/'}${query ? `?${query}` : ''}`
}

/**
 * The current view and a way to change it. A change normally adds a step to
 * the browser's history; with replace it amends the current step, which
 * suits small adjustments such as typing in the search box.
 */
export function useView(): [View, (patch: Partial<View>, replace?: boolean) => void] {
  const [view, setView] = useState(() => parseHash(window.location.hash))

  useEffect(() => {
    const sync = () => setView(parseHash(window.location.hash))
    window.addEventListener('hashchange', sync)
    window.addEventListener('popstate', sync)
    return () => {
      window.removeEventListener('hashchange', sync)
      window.removeEventListener('popstate', sync)
    }
  }, [])

  const go = useCallback((patch: Partial<View>, replace = false) => {
    const next = { ...parseHash(window.location.hash), ...patch }
    const hash = formatHash(next)
    if (hash !== window.location.hash) {
      if (replace) window.history.replaceState(null, '', hash)
      else window.history.pushState(null, '', hash)
    }
    setView(next)
  }, [])

  return [view, go]
}
