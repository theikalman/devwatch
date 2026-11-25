// Shared UI pieces: the html template tag, icons, health pills and change text.
import { h } from './vendor/preact.module.js'
import htm from './vendor/htm.module.js'
import { LABEL } from './format.js'

export const html = htm.bind(h)

// Outline icons in the Lucide style. Functions, so each use gets fresh vnodes.
const PATHS = {
  eye: () => html`<path d="M2.062 12.348a1 1 0 0 1 0-.696 10.75 10.75 0 0 1 19.876 0 1 1 0 0 1 0 .696 10.75 10.75 0 0 1-19.876 0" /><circle cx="12" cy="12" r="3" />`,
  cmd: () => html`<path d="m4 17 6-6-6-6" /><path d="M12 19h8" />`,
  sql: () => html`<ellipse cx="12" cy="5" rx="9" ry="3" /><path d="M3 5v14a9 3 0 0 0 18 0V5" /><path d="M3 12a9 3 0 0 0 18 0" />`,
  http: () => html`<circle cx="12" cy="12" r="10" /><path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20" /><path d="M2 12h20" />`,
  docker: () => html`<path d="M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z" /><path d="m3.3 7 8.7 5 8.7-5" /><path d="M12 22V12" />`,
  port: () => html`<path d="M12 22v-5" /><path d="M9 8V2" /><path d="M15 8V2" /><path d="M18 8v5a4 4 0 0 1-4 4h-4a4 4 0 0 1-4-4V8Z" />`,
  run: () => html`<path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" /><path d="M21 3v5h-5" />`,
  search: () => html`<circle cx="11" cy="11" r="8" /><path d="m21 21-4.3-4.3" />`,
  close: () => html`<path d="M18 6 6 18" /><path d="m6 6 12 12" />`,
  copy: () => html`<rect width="14" height="14" x="8" y="8" rx="2" ry="2" /><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" />`,
  ok: () => html`<circle cx="12" cy="12" r="10" /><path d="m9 12 2 2 4-4" />`,
  warn: () => html`<path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3" /><path d="M12 9v4" /><path d="M12 17h.01" />`,
  crit: () => html`<path d="M2.586 16.726A2 2 0 0 1 2 15.312V8.688a2 2 0 0 1 .586-1.414l4.688-4.688A2 2 0 0 1 8.688 2h6.624a2 2 0 0 1 1.414.586l4.688 4.688A2 2 0 0 1 22 8.688v6.624a2 2 0 0 1-.586 1.414l-4.688 4.688a2 2 0 0 1-1.414.586H8.688a2 2 0 0 1-1.414-.586z" /><path d="m15 9-6 6" /><path d="m9 9 6 6" />`,
  error: () => html`<circle cx="12" cy="12" r="10" /><path d="m4.9 4.9 14.2 14.2" />`,
  stale: () => html`<circle cx="12" cy="12" r="10" /><path d="M12 6v6l4 2" />`,
}

export function Icon({ name, size = 16, stroke = 2 }) {
  const paths = PATHS[name]
  return html`<svg width=${size} height=${size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width=${stroke} stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths && paths()}</svg>`
}

// Pill is a health shown as icon plus word, never color alone.
export const Pill = ({ health }) => html`<span class="pill ${health}"><${Icon} name=${health} size=${12} stroke=${2.4} />${LABEL[health]}</span>`

// Transition is a health change, "OK → Warn", colored by the new health.
export const Transition = ({ from, to }) => html`<span class="pill sm ${to}">${LABEL[from]} → ${LABEL[to]}</span>`

// ChangeText mutes the old side of "old → new".
export function ChangeText({ text }) {
  const parts = String(text || '').split(' → ')
  if (parts.length !== 2) return text
  return html`<span class="old">${parts[0]}</span> → ${parts[1]}`
}
