import preact from '@preact/preset-vite'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'

// ICHOR_API=http://host points `vite` and `vite preview` at a running
// ichor-ui backend, so a local build can be looked at against live data.
const api = process.env.ICHOR_API
const proxyTo = { target: api, changeOrigin: true }
const proxy = api ? { '/ichor.': proxyTo, '/auth/config': proxyTo } : undefined

// https://vite.dev/config/
export default defineConfig({
  server: { proxy },
  preview: { proxy },
  plugins: [preact(), tailwindcss()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  build: {
    // ichor-ui (cmd/ichor-ui) embeds this directory directly with
    // `-tags uiembed` (ADR 0017); no copy step needed.
    outDir: fileURLToPath(new URL('../cmd/ichor-ui/dist', import.meta.url)),
    emptyOutDir: true,
  },
})
