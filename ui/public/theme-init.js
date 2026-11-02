// First paint in the chosen theme (docs/adr/0070; same rule as src/lib/theme.ts).
// A file, not an inline script, so the page's CSP needs no 'unsafe-inline' for scripts (docs/adr/0108).
try {
  var s = localStorage.getItem('ichor-ui-theme')
  var d = s ? s === 'dark' : matchMedia('(prefers-color-scheme: dark)').matches
  document.documentElement.classList.toggle('dark', d)
} catch (e) {}
