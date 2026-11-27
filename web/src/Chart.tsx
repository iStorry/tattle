import { useEffect, useRef, useState } from 'react'
import type { Timeline } from './api'
import { bytes, clock, moment } from './format'

const TOP = 8
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

/**
 * Received rises above the middle line and sent hangs below it, on the same
 * scale, so the two can be compared at a glance and never hide each other.
 */
export function TrafficChart({
  data,
  range,
  height = 220,
  legend = true,
}: {
  data: Timeline
  range: number
  height?: number
  legend?: boolean
}) {
  const [ref, width] = useWidth()
  const [hover, setHover] = useState<number | null>(null)

  const pts = data.points
  const n = pts.length
  const max = niceMax(Math.max(0, ...pts.map((p) => Math.max(p.sent, p.received))))
  const plotH = height - TOP - BOTTOM
  const half = plotH / 2
  const mid = TOP + half
  const slot = n > 0 ? width / n : 0
  const barW = Math.max(1, slot - (slot > 6 ? 2 : 1))
  const scale = (v: number) => (v / max) * half
  const tickEvery = Math.max(1, Math.ceil(n / Math.max(2, Math.floor(width / 110))))
  const active = hover !== null && hover < n ? pts[hover] : null

  return (
    <div className="chart">
      {legend && (
        <div className="legend">
          <span>
            <i className="swatch received" /> Received
          </span>
          <span>
            <i className="swatch sent" /> Sent
          </span>
        </div>
      )}
      <div
        className="plot"
        ref={ref}
        style={{ height }}
        onMouseLeave={() => setHover(null)}
        onMouseMove={(e) => {
          if (!slot) return
          const x = e.clientX - e.currentTarget.getBoundingClientRect().left
          setHover(Math.max(0, Math.min(n - 1, Math.floor(x / slot))))
        }}
      >
        {width > 0 && (
          <svg width={width} height={height} role="img" aria-label="Data received (above the line) and sent (below it) over time">
            <line className="gridline" x1={0} x2={width} y1={TOP} y2={TOP} />
            <line className="gridline" x1={0} x2={width} y1={TOP + plotH} y2={TOP + plotH} />
            {hover !== null && <rect className="hoverband" x={hover * slot} y={TOP} width={slot} height={plotH} />}
            {pts.map((p, i) => {
              const x = i * slot + (slot - barW) / 2
              const hr = scale(p.received)
              const hs = scale(p.sent)
              return (
                <g key={p.t}>
                  {hr > 0.5 && <path className="received" d={bar(x, mid - hr, barW, hr, true)} />}
                  {hs > 0.5 && <path className="sent" d={drop(x, mid, barW, hs)} />}
                </g>
              )
            })}
            <line className="baseline" x1={0} x2={width} y1={mid} y2={mid} />
            <text className="axis" x={4} y={TOP + 12}>
              ↓ {bytes(max)}
            </text>
            <text className="axis" x={4} y={TOP + plotH - 5}>
              ↑ {bytes(max)}
            </text>
            {pts.map(
              (p, i) =>
                i % tickEvery === 0 &&
                i * slot + 60 < width && (
                  <text key={p.t} className="axis" x={i * slot} y={height - 5}>
                    {clock(p.t, range)}
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
              {moment(active.t, range)} – {moment(active.t + data.step, data.step)}
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

/** A bar hanging down from the middle line, rounded at its foot. */
function drop(x: number, y: number, w: number, h: number): string {
  const r = Math.min(3, w / 2, h)
  return `M${x},${y}V${y + h - r}Q${x},${y + h} ${x + r},${y + h}H${x + w - r}Q${x + w},${y + h} ${x + w},${y + h - r}V${y}Z`
}

/** A tiny activity trace for the machine list, filled underneath. */
export function Spark({ values }: { values: number[] }) {
  const max = Math.max(0, ...values)
  if (max <= 0 || values.length < 2) return <svg className="spark" viewBox="0 0 100 24" preserveAspectRatio="none" aria-hidden="true" />
  const step = 100 / (values.length - 1)
  const pts = values.map((v, i) => `${(i * step).toFixed(1)},${(22 - (v / max) * 19).toFixed(1)}`)
  return (
    <svg className="spark" viewBox="0 0 100 24" preserveAspectRatio="none" aria-hidden="true">
      <path className="spark-fill" d={`M0,24L${pts.join('L')}L100,24Z`} />
      <path className="spark-line" d={`M${pts.join('L')}`} />
    </svg>
  )
}
