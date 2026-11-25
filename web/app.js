// devwatch dashboard: one EventSource keeps every card live; the feed and the drawer read history from the API.
import { render } from './vendor/preact.module.js'
import { useEffect, useReducer, useRef, useState } from './vendor/hooks.module.js'
import { ChangeText, html, Icon, Transition } from './ui.js'
import { LABEL, clock, shownHealth } from './format.js'
import { Card } from './cards.js'
import { Drawer } from './drawer.js'

const FEED_MAX = 1000
const feedKey = c => `${c.key}@${c.at}`

function mergeFeed(a, b) {
  const seen = new Set()
  return [...a, ...b]
    .filter(c => !seen.has(feedKey(c)) && seen.add(feedKey(c)))
    .sort((x, y) => y.at - x.at)
    .slice(0, FEED_MAX)
}

const initial = { connected: false, seen: false, title: '', config: '', watchers: [], feed: [], more: false, running: {} }

// reduce mirrors the server hub: a sample event updates its watcher the same way there and here.
function reduce(s, a) {
  switch (a.type) {
    case 'snapshot':
      return { ...s, connected: true, seen: true, title: a.data.title, config: a.data.config, watchers: a.data.watchers, running: {} }
    case 'down':
      return { ...s, connected: false }
    case 'feed':
      return { ...s, feed: mergeFeed(a.items, s.feed), more: a.more }
    case 'run':
      return { ...s, running: { ...s.running, [a.key]: true } }
    case 'ran': {
      const running = { ...s.running }
      delete running[a.key]
      return { ...s, running }
    }
    case 'sample': {
      const ev = a.data
      let feed = s.feed
      const watchers = s.watchers.map(w => {
        if (w.key !== ev.key) return w
        const n = {
          ...w,
          last: { at: ev.at, health: ev.health, ms: ev.ms, num: ev.num, error: ev.error, why: ev.why },
          recent: [...w.recent, { at: ev.at, health: ev.health, num: ev.num }].slice(-60),
          error_since: ev.health === 'error' ? w.error_since || ev.at : 0,
        }
        if ('value' in ev) {
          if (ev.kind === w.kind) Object.assign(n, { prev: w.value, prev_at: w.value_at })
          else delete n.prev
          Object.assign(n, { kind: ev.kind, value: ev.value, value_at: ev.at })
        }
        if (ev.changed) {
          feed = mergeFeed([{ key: w.key, name: w.name, at: ev.at, health: ev.health, prev_health: w.last ? w.last.health : '', summary: ev.summary }], feed)
        }
        return n
      })
      const running = { ...s.running }
      delete running[ev.key]
      return { ...s, watchers, feed, running }
    }
  }
  return s
}

function App() {
  const [s, dispatch] = useReducer(reduce, initial)
  const [now, setNow] = useState(Date.now())
  const [query, setQuery] = useState('')
  const [only, setOnly] = useState(null)
  const [openKey, setOpenKey] = useState(null)
  const filterRef = useRef(null)

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [])

  useEffect(() => {
    const es = new EventSource('/api/events')
    es.addEventListener('snapshot', e => {
      dispatch({ type: 'snapshot', data: JSON.parse(e.data) })
      fetch('/api/changes?limit=50')
        .then(r => r.json())
        .then(d => dispatch({ type: 'feed', items: d.changes, more: d.more }))
        .catch(() => {})
    })
    es.addEventListener('sample', e => dispatch({ type: 'sample', data: JSON.parse(e.data) }))
    es.onerror = () => dispatch({ type: 'down' })
    return () => es.close()
  }, [])

  useEffect(() => {
    const onKey = e => {
      if (e.key === '/' && !/^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement && document.activeElement.tagName)) {
        e.preventDefault()
        filterRef.current && filterRef.current.focus()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  const counts = { ok: 0, warn: 0, crit: 0, error: 0, stale: 0 }
  const healthOf = {}
  for (const w of s.watchers) {
    const h = shownHealth(w, now)
    healthOf[w.key] = h
    if (h) counts[h]++
  }

  useEffect(() => {
    const bad = counts.crit + counts.error
    const marks = [bad && `✖${bad}`, counts.warn && `▲${counts.warn}`].filter(Boolean).join(' ')
    document.title = (marks ? `${marks} · ` : '') + (s.title || 'devwatch')
  })

  const run = key => {
    dispatch({ type: 'run', key })
    const ran = () => dispatch({ type: 'ran', key })
    fetch(`/api/watchers/${encodeURIComponent(key)}/run`, { method: 'POST' })
      .then(r => r.ok || ran())
      .catch(ran)
  }
  const older = () => {
    const oldest = s.feed[s.feed.length - 1]
    fetch(`/api/changes?limit=50&before=${oldest ? oldest.at : Date.now()}`)
      .then(r => r.json())
      .then(d => dispatch({ type: 'feed', items: d.changes, more: d.more }))
      .catch(() => {})
  }

  const q = query.trim().toLowerCase()
  const visible = s.watchers.filter(
    w => (!only || healthOf[w.key] === only) && (!q || `${w.name} ${w.group || ''} ${w.source} ${w.target}`.toLowerCase().includes(q)),
  )
  const groups = []
  const index = {}
  for (const w of visible) {
    const g = w.group || ''
    if (!(g in index)) index[g] = groups.push({ name: g, watchers: [] }) - 1
    groups[index[g]].watchers.push(w)
  }
  const lastAt = Math.max(0, ...s.watchers.map(w => (w.last ? w.last.at : 0)))
  const open = s.watchers.find(w => w.key === openKey)

  return html`
    <header class="top">
      <div class="brand">
        <span class="logo"><${Icon} name="eye" /></span>
        <span class="brand-name">devwatch</span>
        ${s.title && html`<span class="sep">/</span><h1>${s.title}</h1>`}
        ${s.seen && html`<span class="mono meta">${s.config} · ${s.watchers.length} watchers</span>`}
      </div>
      <div class="chips" role="group" aria-label="Filter by health">
        ${s.seen && ['ok', 'warn', 'crit', 'error', 'stale'].filter(h => h === 'ok' || counts[h]).map(h => html`
          <button class="chip ${h}" aria-pressed=${only === h} onClick=${() => setOnly(only === h ? null : h)}><${Icon} name=${h} size=${14} />${counts[h]} ${LABEL[h]}</button>`)}
      </div>
      <div class="grow"></div>
      <div class="search">
        <label for="filter" class="sr-only">Filter watchers</label>
        <span class="search-icon"><${Icon} name="search" /></span>
        <input id="filter" ref=${filterRef} type="search" placeholder="Filter watchers" value=${query} onInput=${e => setQuery(e.currentTarget.value)} />
        <kbd class="mono">/</kbd>
      </div>
      <div class="live ${s.connected ? 'on' : ''}"><span class="dot"></span>${s.connected ? 'Live' : s.seen ? 'Reconnecting' : 'Connecting'}</div>
    </header>
    ${!s.connected && s.seen && html`
      <div class="banner" role="status"><${Icon} name="run" /><strong>Reconnecting to devwatch…</strong>
        <span>Values shown are from ${lastAt ? clock(lastAt) : 'before the connection dropped'} and may be out of date.</span></div>`}
    <div class="page">
      <main class="main">
        ${groups.map(g => html`<section class="group" key=${g.name}>
          ${g.name && html`<h2 class="group-name">${g.name}</h2>`}
          <div class="grid">${g.watchers.map(w => html`<${Card} key=${w.key} w=${w} now=${now} running=${!!s.running[w.key]} onOpen=${() => setOpenKey(w.key)} onRun=${() => run(w.key)} />`)}</div>
        </section>`)}
        ${s.seen && !visible.length && html`<p class="empty">No watchers match the filter.</p>`}
      </main>
      <${Feed} items=${s.feed} more=${s.more} onOpen=${setOpenKey} onMore=${older} />
    </div>
    ${open && html`<${Drawer} key=${open.key} w=${open} now=${now} configName=${s.config} running=${!!s.running[open.key]} onRun=${() => run(open.key)} onClose=${() => setOpenKey(null)} />`}`
}

function Feed({ items, more, onOpen, onMore }) {
  return html`<aside class="feed" aria-labelledby="feed-title">
    <div class="feed-head"><h2 id="feed-title">Changes</h2><span class="meta">${items.length}${more ? '+' : ''}</span></div>
    ${items.length
      ? html`<ol class="feed-list">${items.map(c => html`<li key=${feedKey(c)}>
          <button class="feed-item" onClick=${() => onOpen(c.key)}>
            <span class="mono t">${clock(c.at)}</span>
            <span class="feed-main">
              <span class="feed-name">${c.name}${c.prev_health && c.prev_health !== c.health && html`<${Transition} from=${c.prev_health} to=${c.health} />`}</span>
              ${c.summary !== `${c.prev_health} → ${c.health}` && html`<span class="mono feed-sum"><${ChangeText} text=${c.summary} /></span>`}
            </span>
          </button>
        </li>`)}</ol>`
      : html`<p class="feed-empty">No changes yet. A line appears here as soon as a value or a health changes.</p>`}
    ${more && html`<div class="feed-foot"><button class="btn wide" onClick=${onMore}>Show older changes</button></div>`}
  </aside>`
}

render(html`<${App} />`, document.getElementById('app'))
