import type { ReactNode } from 'react'

// Line icons drawn on a 24px grid in the text colour. They are decoration:
// whatever they stand for is always also said in words nearby.

const SHAPES = {
  dashboard: (
    <>
      <rect x="3.5" y="3.5" width="7" height="7" rx="1.5" />
      <rect x="13.5" y="3.5" width="7" height="7" rx="1.5" />
      <rect x="3.5" y="13.5" width="7" height="7" rx="1.5" />
      <rect x="13.5" y="13.5" width="7" height="7" rx="1.5" />
    </>
  ),
  topology: (
    <>
      <circle cx="12" cy="5" r="2.5" />
      <circle cx="5" cy="19" r="2.5" />
      <circle cx="19" cy="19" r="2.5" />
      <path d="M12 7.5v4M12 11.5 5.8 16.8M12 11.5l6.2 5.3" />
    </>
  ),
  health: <path d="M3 12h4l2-5 4 10 2-5h6" />,
  settings: (
    <>
      <circle cx="12" cy="12" r="3" />
      <path d="M12 2.5v3M12 18.5v3M2.5 12h3M18.5 12h3M5.3 5.3l2.1 2.1M16.6 16.6l2.1 2.1M5.3 18.7l2.1-2.1M16.6 7.4l2.1-2.1" />
    </>
  ),
  signout: (
    <>
      <path d="M14 4.5h4a1.5 1.5 0 0 1 1.5 1.5v12a1.5 1.5 0 0 1-1.5 1.5h-4" />
      <path d="M10 8l-4 4 4 4M6 12h9" />
    </>
  ),
  sun: (
    <>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M5.3 18.7l1.4-1.4M17.3 6.7l1.4-1.4" />
    </>
  ),
  moon: <path d="M19.5 14.5A8 8 0 0 1 9.5 4.5a8 8 0 1 0 10 10z" />,
  close: <path d="M6 6l12 12M18 6 6 18" />,
  search: (
    <>
      <circle cx="11" cy="11" r="6.5" />
      <path d="m16 16 4.5 4.5" />
    </>
  ),
  check: <path d="m5 12.5 4.5 4.5L19 7.5" />,
  alert: (
    <>
      <path d="M12 4 21 19.5H3z" />
      <path d="M12 10v4.5M12 17.2h.01" />
    </>
  ),
  out: <path d="M7 17 17 7M9 7h8v8" />,
  in: <path d="M17 7 7 17M7 9v8h8" />,
  internet: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M3.5 12h17M12 3.5c3 3.2 3 13.8 0 17M12 3.5c-3 3.2-3 13.8 0 17" />
    </>
  ),
  router: (
    <>
      <rect x="3.5" y="13" width="17" height="6.5" rx="2" />
      <path d="M7.5 16.3h.01M11 16.3h.01M8 13V8.5M16 13V5.5" />
    </>
  ),
  host: (
    <>
      <rect x="4" y="4" width="16" height="6.5" rx="1.8" />
      <rect x="4" y="13.5" width="16" height="6.5" rx="1.8" />
      <path d="M7.5 7.3h.01M7.5 16.8h.01" />
    </>
  ),
  vm: (
    <>
      <rect x="3.5" y="4.5" width="17" height="11.5" rx="2" />
      <path d="M9 20h6M12 16v4" />
    </>
  ),
  ct: (
    <>
      <path d="M12 3.5 20 8v8l-8 4.5L4 16V8z" />
      <path d="M4 8l8 4.5L20 8M12 12.5v8" />
    </>
  ),
  device: (
    <>
      <rect x="7" y="3.5" width="10" height="17" rx="2.2" />
      <path d="M11 17.5h2" />
    </>
  ),
} satisfies Record<string, ReactNode>

export type IconName = keyof typeof SHAPES

export function Icon({ name, className = 'icon' }: { name: IconName; className?: string }) {
  return (
    <svg className={className} viewBox="0 0 24 24" aria-hidden="true">
      {SHAPES[name]}
    </svg>
  )
}
