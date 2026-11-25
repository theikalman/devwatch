# Vendored files

Copied from the npm registry so the UI works offline and needs no build step. Licenses sit next to the files.

| File | Package | License |
|---|---|---|
| `preact.module.js` | `preact@10.29.8` `dist/preact.module.js` | MIT (`LICENSE-preact`) |
| `hooks.module.js` | `preact@10.29.8` `hooks/dist/hooks.module.js` | MIT (`LICENSE-preact`) |
| `htm.module.js` | `htm@3.1.1` `dist/htm.module.js` | Apache-2.0 (`LICENSE-htm`) |
| `fonts/inter-latin-wght-normal.woff2` | `@fontsource-variable/inter@5.3.0` | OFL-1.1 (`fonts/LICENSE-inter`) |
| `fonts/jetbrains-mono-latin-wght-normal.woff2` | `@fontsource-variable/jetbrains-mono@5.3.0` | OFL-1.1 (`fonts/LICENSE-jetbrains-mono`) |

Changes from upstream: the `sourceMappingURL` comments are removed, and `hooks.module.js` imports
`./preact.module.js` instead of the bare `preact`, so no import map (an inline script) is needed.
