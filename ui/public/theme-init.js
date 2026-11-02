// First paint in the chosen theme (same rule as src/lib/theme.ts).
// A file, not an inline script, so the page's CSP needs no 'unsafe-inline' for scripts.
try {
  var s = localStorage.getItem('ichor-ui-theme')
  var d = s ? s === 'dark' : matchMedia('(prefers-color-scheme: dark)').matches
  document.documentElement.classList.toggle('dark', d)
} catch (e) {}
