// The watcher detail drawer: current value and why, history chart, change log, settings.
import { useEffect, useRef, useState } from './vendor/hooks.module.js'
import { ChangeText, html, Icon, Pill, Transition } from './ui.js'
import { LABEL, clock, dur, hm, num, short, shownHealth, signed, threshold } from './format.js'

const RANGES = [['15m', 15 * 60e3], ['1h', 60 * 60e3], ['24h', 24 * 60 * 60e3]]

export function Drawer({ w, now, configName, running, onRun, onClose }) {
  const [range, setRange] = useState(RANGES[1][1])
  const [rows, setRows] = useState(null)
  const [copied, setCopied] = useState(false)
  const closeRef = useRef(null)
  const req = useRef({ n: 0, at: 0 })

  const load = () => {
    const n = ++req.current.n
    req.current.at = Date.now()
    fetch(`/api/watchers/${encodeURIComponent(w.key)}/samples?since=${Date.now() - range}`)
      .then(r => (r.ok ? r.json() : Promise.reject(r.status)))
      .then(d => n === req.current.n && setRows(d.samples))
      .catch(() => n === req.current.n && setRows([]))
  }
  useEffect(load, [w.key, range])
  // Refresh with new checks, at most every 5s so long ranges aren't refetched every second.
  useEffect(() => {
    if (Date.now() - req.current.at > 5000) load()
  }, [w.last && w.last.at])

  useEffect(() => {
    closeRef.current && closeRef.current.focus({ preventScroll: true })
    const onKey = e => e.key === 'Escape' && onClose()
    document.addEventListener('keydown', onKey)
    document.body.classList.add('noscroll')
    return () => {
      document.removeEventListener('keydown', onKey)
      document.body.classList.remove('noscroll')
    }
  }, [])

  const health = shownHealth(w, now)
  const copy = () =>
    navigator.clipboard &&
    navigator.clipboard.writeText(typeof w.value === 'string' ? w.value : JSON.stringify(w.value, null, 2)).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })

  return html`
    <div class="scrim" onClick=${onClose}></div>
    <section class="drawer" role="dialog" aria-modal="true" aria-labelledby="d-title">
      <header class="drawer-head">
        <span class="card-icon"><${Icon} name=${w.source} size=${18} /></span>
        <div class="grow">
          <div class="drawer-title"><h2 id="d-title">${w.name}</h2>${health && html`<${Pill} health=${health} />`}</div>
          <div class="card-source mono">${w.source} · ${w.target}</div>
        </div>
        <button ref=${closeRef} class="icon-btn" aria-label="Close" onClick=${onClose}><${Icon} name="close" size=${18} /></button>
      </header>
      <div class="drawer-body">
        <div class="drawer-now">
          <div class="grow">
            ${w.value !== undefined && html`<div class="value-line">
              <span class="mono value-xl ${health === 'warn' || health === 'crit' ? `tone-${health}` : ''} ${health === 'error' || health === 'stale' ? 'dim' : ''}">${short(w.kind, w.value)}</span>
              ${w.unit && html`<span class="unit">${w.unit}</span>`}
            </div>`}
            <p class="reason">${reason(w, health, now)}</p>
          </div>
          <div class="actions">
            <button class="btn primary" disabled=${running} onClick=${onRun}><${Icon} name="run" />${running ? 'Running…' : 'Run now'}</button>
            ${w.value !== undefined && html`<button class="btn" onClick=${copy}><${Icon} name="copy" />${copied ? 'Copied' : 'Copy value'}</button>`}
          </div>
        </div>

        <section aria-labelledby="h-history">
          <div class="section-head">
            <h3 id="h-history">History</h3>
            <div class="seg" role="group" aria-label="Time range">
              ${RANGES.map(([label, ms]) => html`<button aria-pressed=${range === ms} onClick=${() => setRange(ms)}>${label}</button>`)}
            </div>
          </div>
          ${rows === null ? html`<p class="muted chart-empty">Loading…</p>` : html`<${Chart} rows=${rows} w=${w} since=${now - range} now=${now} /><${Stats} rows=${rows} />`}
        </section>

        <section aria-labelledby="h-changes">
          <h3 id="h-changes">Changes</h3>
          ${rows && html`<${ChangeLog} rows=${rows} />`}
        </section>

        <section aria-labelledby="h-spec">
          <h3 id="h-spec">Watcher</h3>
          <pre class="spec mono">${w.spec}</pre>
          <p class="note">From ${configName}. Env values and secret-looking settings are hidden.</p>
        </section>
      </div>
    </section>`
}

function reason(w, health, now) {
  if (!w.last) return 'Waiting for the first check.'
  if (w.last.health === 'error') return html`<span class="errtext mono">${w.last.error}</span> · failing for ${dur(now - (w.error_since || w.last.at))}`
  if (health === 'stale') return `No check for ${dur(now - w.last.at)}; expected every ${dur(w.every)}.`
  const parts = []
  if (w.last.why) parts.push(`${LABEL[w.last.health]} because ${w.last.why}.`)
  if (w.kind === 'number' && typeof w.prev === 'number') parts.push(`${signed(w.value - w.prev)} since ${clock(w.value_at)}.`)
  else if (w.value_at) parts.push(`This value since ${clock(w.value_at)}.`)
  return parts.join(' ')
}

// niceStep rounds a raw step up to 1, 2, 2.5 or 5 times a power of ten, so axis labels read as round numbers.
function niceStep(raw) {
  const p = 10 ** Math.floor(Math.log10(raw))
  return [1, 2, 2.5, 5, 10].find(m => m * p >= raw) * p
}

function Chart({ rows, w, since: rangeStart, now }) {
  const [hover, setHover] = useState(null)
  const pts = rows.filter(r => r.num != null)
  if (pts.length < 2) {
    const none = w.kind === 'text' || w.kind === 'json'
    return html`<p class="muted chart-empty">${none ? 'This watcher has no number to chart; its changes are listed below.' : 'Not enough checks in this range yet.'}</p>`
  }
  const W = 512, H = 200, L = 40, R = 504, T = 12, B = 172
  const since = Math.max(rangeStart, pts[0].at) // a shorter history fills the width instead of hugging the right edge
  const limit = threshold(w)
  const vals = pts.map(p => p.num)
  const min = Math.min(...vals), max = Math.max(...vals)
  const step = niceStep(((limit ? Math.max(max, limit.value) : max) - (limit ? Math.min(min, limit.value) : min)) / 2 || Math.abs(max) / 2 || 1)
  const lo = Math.floor((limit ? Math.min(min, limit.value) : min) / step) * step
  const hi = Math.max(Math.ceil((limit ? Math.max(max, limit.value) : max) / step) * step, lo + step)
  const ticks = []
  for (let v = lo; v <= hi + step / 2; v += step) ticks.push(v)
  const x = t => L + ((t - since) / (now - since || 1)) * (R - L)
  const at = now - since < 10 * 60e3 ? clock : hm // seconds matter on short spans
  const y = v => B - ((v - lo) / (hi - lo)) * (B - T)
  let d = ''
  pts.forEach((p, i) => (d += i ? ` H${x(p.at).toFixed(1)} V${y(p.num).toFixed(1)}` : `M${x(p.at).toFixed(1)},${y(p.num).toFixed(1)}`))
  d += ` H${x(now).toFixed(1)}`

  const move = e => {
    const box = e.currentTarget.getBoundingClientRect()
    const t = since + (((e.clientX - box.left) / box.width) * W - L) / (R - L) * (now - since)
    let p = pts[0]
    for (const q of pts) if (q.at <= t) p = q
    setHover(p)
  }
  const hx = hover && x(hover.at), hy = hover && y(hover.num)
  const tipX = hover && (hx > W / 2 ? hx - 136 : hx + 8)

  return html`<svg class="chart" viewBox="0 0 ${W} ${H}" onMouseMove=${move} onMouseLeave=${() => setHover(null)}
      role="img" aria-label="${w.name}: from ${num(min)} to ${num(max)} between ${hm(since)} and ${hm(now)}">
    ${ticks.map(v => html`<line class="grid" x1=${L} x2=${R} y1=${y(v)} y2=${y(v)} /><text class="axis" x=${L - 8} y=${y(v) + 4} text-anchor="end">${num(Number(v.toPrecision(6)))}</text>`)}
    ${limit && html`<line class="limit" x1=${L} x2=${R} y1=${y(limit.value)} y2=${y(limit.value)} /><text class="limit-label" x=${L + 4} y=${y(limit.value) - 5}>${limit.name} ${limit.rule}</text>`}
    <path class="area" d=${`${d} V${B} H${x(pts[0].at).toFixed(1)} Z`} />
    <path class="line" d=${d} />
    ${hover && html`
      <line class="cursor" x1=${hx} x2=${hx} y1=${T} y2=${B} />
      <circle class="dot" cx=${hx} cy=${hy} r="4" />
      <rect class="tip" x=${tipX} y="4" width="128" height="40" rx="6" />
      <text class="tip-text" x=${tipX + 12} y="20">${clock(hover.at)}</text>
      <text class="tip-value" x=${tipX + 12} y="36">${num(hover.num)}${w.unit ? ` ${w.unit}` : ''}</text>`}
    <text class="axis" x=${L} y=${H - 6}>${at(since)}</text>
    <text class="axis" x=${(L + R) / 2} y=${H - 6} text-anchor="middle">${at((since + now) / 2)}</text>
    <text class="axis" x=${R} y=${H - 6} text-anchor="end">${at(now)}</text>
  </svg>`
}

function Stats({ rows }) {
  const nums = rows.map(r => r.num).filter(n => n != null)
  const failed = rows.filter(r => r.health === 'error').length
  return html`<dl class="stats">
    <div><dt>Min</dt><dd class="mono">${nums.length ? num(Math.min(...nums)) : '—'}</dd></div>
    <div><dt>Max</dt><dd class="mono">${nums.length ? num(Math.max(...nums)) : '—'}</dd></div>
    <div><dt>Changes</dt><dd class="mono">${rows.filter(r => r.changed).length}</dd></div>
    <div><dt>Checks</dt><dd class="mono">${rows.length}<small> · ${failed} failed</small></dd></div>
  </dl>`
}

function ChangeLog({ rows }) {
  const items = []
  let prev = ''
  for (const r of rows) {
    if (r.changed) items.push({ ...r, prev })
    prev = r.health
  }
  if (!items.length) return html`<p class="muted">No changes in this range.</p>`
  return html`<ol class="log">${items.reverse().slice(0, 200).map(c => html`<li key=${c.at}>
    <span class="mono t">${clock(c.at)}</span>
    <span class="mono grow"><${ChangeText} text=${c.summary} /></span>
    ${c.prev && c.prev !== c.health && html`<${Transition} from=${c.prev} to=${c.health} />`}
  </li>`)}</ol>`
}
