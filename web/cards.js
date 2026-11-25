// A card per watcher: one frame, one body per value kind.
import { useState } from './vendor/hooks.module.js'
import { html, Icon, Pill } from './ui.js'
import { ago, clock, dur, num, plural, rulesText, short, shownHealth, signed, threshold } from './format.js'

const WIDE = new Set(['table', 'json'])
const LOADED = Date.now() // only values that change after the page loaded flash

const flash = w => (w.value_at > LOADED ? 'flash' : '')
const tone = health => (health === 'warn' || health === 'crit' ? `tone-${health}` : '')

export function Card({ w, now, running, onOpen, onRun }) {
  const health = shownHealth(w, now)
  // The whole card opens the drawer for the mouse; the name is the button for the keyboard.
  const click = () => window.getSelection().toString() || onOpen()
  const stop = fn => e => { e.stopPropagation(); fn() }
  return html`<article class="card ${health || 'loading'} ${WIDE.has(w.kind) ? 'wide' : ''}" onClick=${click}>
    <header class="card-head">
      <span class="card-icon"><${Icon} name=${w.source} /></span>
      <div class="card-title">
        <h3><button class="card-open" onClick=${stop(onOpen)}>${w.name}</button></h3>
        <div class="card-source mono" title=${w.target}>${w.source} · ${w.target}</div>
      </div>
      ${health && html`<${Pill} health=${health} />`}
    </header>
    <div class="card-body">${body(w, health, now)}</div>
    <footer class="card-foot">
      <span class="grow">${footer(w, health, now)}</span>
      <button class="run ${running ? 'spinning' : ''}" aria-label="Run ${w.name} now" title="Run now" disabled=${running} onClick=${stop(onRun)}><${Icon} name="run" /></button>
    </footer>
  </article>`
}

function body(w, health, now) {
  if (!w.last) return html`<div class="skel big"></div><div class="skel line"></div>`
  if (w.last.health === 'error') return html`<${ErrorBody} w=${w} />`
  const Body = BODIES[w.kind]
  if (!Body) return null
  const content = html`<${Body} w=${w} health=${health} now=${now} />`
  if (health !== 'stale') return content
  return html`<div class="dim">${content}</div><p class="sub">no check for ${dur(now - w.last.at)} (expected every ${dur(w.every)})</p>`
}

function footer(w, health, now) {
  if (!w.last) return 'waiting for first check'
  if (w.last.health === 'error') return `failing for ${dur(now - (w.error_since || w.last.at))} · every ${dur(w.every)}`
  if (health === 'stale') return `last checked ${clock(w.last.at)}`
  return `checked ${ago(w.last.at, now)} · ${dur(w.last.ms)} · every ${dur(w.every)}`
}

const Unit = ({ w }) => w.unit && html`<span class="unit">${w.unit}</span>`
const Rules = ({ w }) => rulesText(w) && html`<div class="caption mono">${rulesText(w)}</div>`

function NumberBody({ w, health, now }) {
  const prev = typeof w.prev === 'number' ? w.prev : null
  const line = threshold(w)
  return html`
    <div class="split">
      <div>
        <div class="value-line"><span key=${w.value_at} class="mono value-l ${tone(health)} ${flash(w)}">${num(w.value)}</span><${Unit} w=${w} /></div>
        ${prev === null
          ? html`<div class="sub">unchanged for ${dur(now - w.value_at)}</div>`
          : html`<div class="delta mono">${signed(w.value - prev)} since ${clock(w.value_at)}</div>`}
      </div>
      <${Sparkline} values=${w.recent.map(p => p.num)} line=${line && line.value} health=${health} />
    </div>
    <${Rules} w=${w} />`
}

function TextBody({ w }) {
  const v = String(w.value)
  return html`
    ${v.includes('\n')
      ? html`<pre key=${w.value_at} class="text mono ${flash(w)}" onClick=${e => e.stopPropagation()}>${v}</pre>`
      : html`<div key=${w.value_at} class="mono value-s ${flash(w)}">${v || html`<span class="muted">(empty)</span>`}</div>`}
    <div class="sub">since ${clock(w.value_at)}${w.prev !== undefined && html` · was <span class="mono">${short('text', w.prev)}</span>`}</div>
    <${Rules} w=${w} />`
}

function TableBody({ w }) {
  const t = w.value
  const key = r => r.join('\t')
  const prev = w.prev && Array.isArray(w.prev.rows) && key(w.prev.cols) === key(t.cols) ? w.prev : null
  const before = prev && new Set(prev.rows.map(key))
  const current = new Set(t.rows.map(key))
  const gone = prev ? prev.rows.filter(r => !current.has(key(r))) : []
  return html`
    <div class="tablewrap" onClick=${e => e.stopPropagation()}>
      <table class="t">
        <thead><tr>${t.cols.map(c => html`<th>${c}</th>`)}</tr></thead>
        <tbody>
          ${t.rows.map(r => {
            const isNew = before && !before.has(key(r))
            return html`<tr class=${isNew ? 'new' : ''}>${r.map((c, i) => html`<td class="mono">${c}${isNew && i === 0 && html`<span class="tag-new">NEW</span>`}</td>`)}</tr>`
          })}
          ${gone.map(r => html`<tr class="gone">${r.map(c => html`<td class="mono">${c}</td>`)}</tr>`)}
        </tbody>
      </table>
    </div>
    <div class="sub">${plural(t.rows.length, 'row')}${t.truncated ? ' (first 200 kept)' : ''}${rulesText(w) && ` · ${rulesText(w)}`}</div>`
}

function JsonBody({ w }) {
  const changed = new Map()
  if (w.prev !== undefined) diff(w.prev, w.value, '', changed)
  return html`<div class="json mono" onClick=${e => e.stopPropagation()}><${JsonNode} v=${w.value} path="" changed=${changed} depth=${0} /></div>`
}

const isObject = v => v !== null && typeof v === 'object' && !Array.isArray(v)

// diff records each changed path with its old value; objects are compared key by key.
function diff(a, b, path, out) {
  if (isObject(a) && isObject(b)) {
    for (const k of new Set([...Object.keys(a), ...Object.keys(b)])) diff(a[k], b[k], path ? `${path}.${k}` : k, out)
  } else if (JSON.stringify(a) !== JSON.stringify(b)) {
    out.set(path, a)
  }
}

const jsonText = v => (v === undefined ? 'absent' : JSON.stringify(v))

function JsonNode({ k, v, path, changed, depth }) {
  const inside = [...changed.keys()].some(p => p === path || path === '' || p.startsWith(path + '.'))
  const [open, setOpen] = useState(depth < 1 || (changed.size > 0 && inside))
  const pad = { paddingLeft: `${depth * 16}px` }
  const name = k !== undefined && html`<span class="k">${k}</span>`
  const isChanged = changed.has(path)
  if (v === null || typeof v !== 'object') {
    return html`<div class="row ${isChanged ? 'changed' : ''}" style=${pad}>${name}${name && ': '}${jsonText(v)}${isChanged && html`<span class="old">${jsonText(changed.get(path))}</span>`}</div>`
  }
  const entries = Array.isArray(v) ? v.map((x, i) => [String(i), x]) : Object.entries(v)
  return html`<div>
    <div class="row ${isChanged ? 'changed' : ''}" style=${pad}>
      <button class="tog" aria-expanded=${open} aria-label=${open ? 'Collapse' : 'Expand'} onClick=${() => setOpen(!open)}>${open ? '▾' : '▸'}</button>
      ${name} <span class="muted">${Array.isArray(v) ? `[${entries.length}]` : `{${entries.length}}`}</span>
    </div>
    ${open && entries.map(([ck, cv]) => html`<${JsonNode} key=${ck} k=${ck} v=${cv} path=${path ? `${path}.${ck}` : ck} changed=${changed} depth=${depth + 1} />`)}
  </div>`
}

function ProbeBody({ w }) {
  const p = w.value
  const latency = w.last && w.last.num
  const detail = p.up
    ? [p.code || (w.source === 'port' ? 'open' : ''), latency != null && `${num(latency)} ms`].filter(Boolean).join(' · ')
    : `${p.reason || 'down'} · since ${clock(w.value_at)}`
  return html`
    <div class="split">
      <div>
        <div class="mono value-m ${p.up ? 'tone-ok' : 'tone-crit'}">${p.up ? 'UP' : 'DOWN'}</div>
        <div class="sub mono">${detail}</div>
      </div>
      ${p.up && html`<${Sparkline} values=${w.recent.map(r => r.num)} height=${32} label="Latency" />`}
    </div>
    <${Uptime} recent=${w.recent} />`
}

function ErrorBody({ w }) {
  const good = [...w.recent].reverse().find(p => p.health !== 'error')
  return html`
    <div class="errbox mono">${w.last.error}</div>
    ${w.value !== undefined && html`
      <div class="value-line top dim"><span class="mono value-m">${short(w.kind, w.value)}</span><${Unit} w=${w} /></div>
      <div class="sub">last good value${good ? ` · ${clock(good.at)}` : ''}</div>`}`
}

const BODIES = { number: NumberBody, text: TextBody, table: TableBody, json: JsonBody, probe: ProbeBody }

export function Sparkline({ values, line = null, health, width = 112, height = 44, label = 'Value' }) {
  const pts = values.map((v, i) => [i, v]).filter(([, v]) => v != null)
  if (pts.length < 2) return null
  const vals = pts.map(p => p[1])
  let lo = Math.min(...vals), hi = Math.max(...vals)
  if (line != null) [lo, hi] = [Math.min(lo, line), Math.max(hi, line)]
  if (lo === hi) [lo, hi] = [lo - 1, hi + 1]
  const x = i => 2 + (i / (values.length - 1)) * (width - 4)
  const y = v => height - 4 - ((v - lo) / (hi - lo)) * (height - 8)
  const [li, lv] = pts[pts.length - 1]
  const color = health === 'warn' ? 'var(--warn)' : health === 'crit' ? 'var(--crit)' : 'var(--accent)'
  return html`<svg class="spark" width=${width} height=${height} viewBox="0 0 ${width} ${height}" style=${{ color }}
      role="img" aria-label="${label}, last ${values.length} checks: from ${num(Math.min(...vals))} to ${num(Math.max(...vals))}">
    ${line != null && html`<line x1="0" x2=${width} y1=${y(line)} y2=${y(line)} stroke="currentColor" stroke-dasharray="3 3" opacity=".6" />`}
    <polyline points=${pts.map(([i, v]) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(' ')} fill="none" stroke="currentColor" stroke-width="1.75" stroke-linejoin="round" stroke-linecap="round" />
    <circle cx=${x(li)} cy=${y(lv)} r="2.5" fill="currentColor" />
  </svg>`
}

function Uptime({ recent }) {
  const pts = recent.slice(-30)
  const up = pts.filter(p => p.health === 'ok' || p.health === 'warn').length
  return html`
    <div class="uptime" role="img" aria-label="Last ${pts.length} checks: ${up} up">${pts.map(p => html`<span class=${p.health}></span>`)}</div>
    <div class="strip-caption"><span>last ${pts.length} checks</span><span>${pts.length ? Math.round((up / pts.length) * 100) : 0}% up</span></div>`
}
