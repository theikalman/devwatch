// Formatting shared by the cards, the feed and the drawer. Times are unix milliseconds.

export const LABEL = { ok: 'OK', warn: 'Warn', crit: 'Crit', error: 'Error', stale: 'Stale' }

// clock is an event time: 12:03:05.
export const clock = ms => new Date(ms).toLocaleTimeString('en-GB', { hour12: false })

// hm is an axis time: 12:03.
export const hm = ms => new Date(ms).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', hour12: false })

// dur is a short duration: 14 ms, 1.8s, 3m 12s, 41m, 2h 5m.
export function dur(ms) {
  if (ms < 1) return '<1 ms'
  if (ms < 1000) return `${Math.round(ms)} ms`
  if (ms < 10_000) return `${(ms / 1000).toFixed(1).replace(/\.0$/, '')}s`
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 10) return s % 60 ? `${m}m ${s % 60}s` : `${m}m`
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  return m % 60 ? `${h}h ${m % 60}m` : `${h}h`
}

// ago is freshness: "just now", "2s ago", "3m 12s ago".
export function ago(ms, now) {
  const d = now - ms
  if (d < 1500) return 'just now'
  return (d < 60_000 ? `${Math.round(d / 1000)}s` : dur(d)) + ' ago'
}

// Numbers are shown as the source returned them: no thousands separators, which would garble years, ports and PIDs.
const nf = new Intl.NumberFormat('en-US', { useGrouping: false, maximumFractionDigits: 6 })
export const num = n => nf.format(n)

// signed is a delta with a real minus sign, rounded to hide float noise.
export function signed(d) {
  const r = Math.round(d * 1e9) / 1e9
  return (r < 0 ? '−' : '+') + num(Math.abs(r))
}

export const plural = (n, unit) => `${n} ${unit}${n === 1 ? '' : 's'}`

const cut = (s, n) => (s.length > n ? s.slice(0, n - 1) + '…' : s)

// short is a value on one line, for "was …", stale values and the drawer.
export function short(kind, v) {
  switch (kind) {
    case 'number':
      return num(v)
    case 'text': {
      const [first, ...rest] = String(v).split('\n')
      if (rest.length) return `${cut(first, 80)} (+${rest.length} lines)`
      return cut(first, 80) || '(empty)'
    }
    case 'table':
      return plural(v.rows.length, 'row')
    case 'probe':
      return v.up ? (v.code ? `UP ${v.code}` : 'UP') : 'DOWN'
  }
  return cut(JSON.stringify(v), 80)
}

// shownHealth is the health a card shows: its last check's, or stale when checks stopped arriving.
export function shownHealth(w, now) {
  if (!w.last) return null
  if (now - (w.last.at + w.last.ms) > 3 * w.every + w.timeout) return 'stale'
  return w.last.health
}

// threshold is the number of the first numeric warn or crit rule, for drawing it on charts.
export function threshold(w) {
  for (const k of ['warn', 'crit']) {
    const m = /^(?:>=?|<=?)\s*(-?\d+(?:\.\d+)?)$/.exec(w.rules?.[k] || '')
    if (m) return { name: k, rule: w.rules[k], value: +m[1] }
  }
  return null
}

export const rulesText = w => ['warn', 'crit', 'expect'].filter(k => w.rules?.[k]).map(k => `${k} ${w.rules[k]}`).join(' · ')
