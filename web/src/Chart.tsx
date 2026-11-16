import { useEffect, useRef, useState } from 'react'
import type { Timeline } from './api'
import { bytes, clock } from './format'

const HEIGHT = 190
const TOP = 18
const BOTTOM = 22

/** Rounds a value up to a 1/2/5 step so the axis ends on a readable number. */
function niceMax(v: number): number {
  if (v <= 0) return 1024
  const unit = 1024 ** Math.floor(Math.log(v) / Math.log(1024))
  const m = v / unit
  const step = [1, 2, 5, 10, 20, 50, 100, 200, 500, 1024].find((s) => s >= m) ?? 1024
  return step * unit
}

/** A bar with a rounded top and a square foot, so it sits flat on the baseline. */
function bar(x: number, y: number, w: number, h: number, round: boolean): string {
  const r = round ? Math.min(3, w / 2, h) : 0
  return `M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h}Z`
}

function useWidth() {
  const ref = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(0)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const ro = new ResizeObserver(([entry]) => setWidth(Math.floor(entry.contentRect.width)))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  return [ref, width] as const
}

export function TrafficChart({ data, range }: { data: Timeline; range: number }) {
  const [ref, width] = useWidth()
  const [hover, setHover] = useState<number | null>(null)

  const pts = data.points
  const n = pts.length
  const max = niceMax(Math.max(0, ...pts.map((p) => p.sent + p.received)))
  const plotH = HEIGHT - TOP - BOTTOM
  const slot = n > 0 ? width / n : 0
  const barW = Math.max(1, slot - 2)
  const scale = (v: number) => (v / max) * plotH
  const withDay = range > 86400
  const tickEvery = Math.max(1, Math.ceil(n / Math.max(2, Math.floor(width / 110))))
  const active = hover !== null && hover < n ? pts[hover] : null

  return (
    <div className="chart">
      <div className="legend">
        <span>
          <i className="swatch received" /> Received
        </span>
        <span>
          <i className="swatch sent" /> Sent
        </span>
      </div>
      <div
        className="plot"
        ref={ref}
        onMouseLeave={() => setHover(null)}
        onMouseMove={(e) => {
          if (!slot) return
          const x = e.clientX - e.currentTarget.getBoundingClientRect().left
          setHover(Math.max(0, Math.min(n - 1, Math.floor(x / slot))))
        }}
      >
        {width > 0 && (
          <svg width={width} height={HEIGHT} role="img" aria-label="Data sent and received over time">
            {[1, 0.5].map((f) => (
              <g key={f}>
                <line className="grid" x1={0} x2={width} y1={TOP + plotH * (1 - f)} y2={TOP + plotH * (1 - f)} />
                <text className="axis" x={0} y={TOP + plotH * (1 - f) - 5}>
                  {bytes(max * f)}
                </text>
              </g>
            ))}
            <line className="baseline" x1={0} x2={width} y1={TOP + plotH} y2={TOP + plotH} />
            {hover !== null && <rect className="hoverband" x={hover * slot} y={TOP} width={slot} height={plotH} />}
            {pts.map((p, i) => {
              const x = i * slot + (slot - barW) / 2
              const hr = scale(p.received)
              const hs = scale(p.sent)
              // A 2px gap separates the stacked segments when both are tall enough to show one.
              const gap = hr >= 3 && hs >= 3 ? 2 : 0
              const base = TOP + plotH
              return (
                <g key={p.t}>
                  {hr > 0.5 && <path className="received" d={bar(x, base - hr, barW, hr, hs <= 0.5)} />}
                  {hs > 0.5 && <path className="sent" d={bar(x, base - hr - hs, barW, hs - gap, true)} />}
                </g>
              )
            })}
            {pts.map(
              (p, i) =>
                i % tickEvery === 0 &&
                i * slot + 60 < width && (
                  <text key={p.t} className="axis" x={i * slot} y={HEIGHT - 5}>
                    {clock(p.t, withDay)}
                  </text>
                ),
            )}
          </svg>
        )}
        {active && hover !== null && (
          <div
            className="tooltip"
            style={hover * slot > width / 2 ? { right: width - hover * slot + 8 } : { left: (hover + 1) * slot + 8 }}
          >
            <div className="tooltip-title">
              {clock(active.t, withDay)} – {clock(active.t + data.step, false)}
            </div>
            <div>
              <i className="swatch received" /> Received <b>{bytes(active.received)}</b>
            </div>
            <div>
              <i className="swatch sent" /> Sent <b>{bytes(active.sent)}</b>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

/** A tiny activity trace for the machine list. */
export function Spark({ values }: { values: number[] }) {
  const max = Math.max(...values)
  if (max <= 0) return <svg className="spark" viewBox="0 0 60 18" aria-hidden="true" />
  const step = 60 / (values.length - 1)
  const d = values.map((v, i) => `${i === 0 ? 'M' : 'L'}${(i * step).toFixed(1)},${(16 - (v / max) * 14).toFixed(1)}`).join('')
  return (
    <svg className="spark" viewBox="0 0 60 18" aria-hidden="true">
      <path d={d} />
    </svg>
  )
}
