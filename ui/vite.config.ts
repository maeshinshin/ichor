import preact from '@preact/preset-vite'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [preact(), tailwindcss()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  build: {
    // ichor-ui (cmd/ichor-ui) embeds this directory directly with
    // `-tags uiembed`; no copy step needed.
    outDir: fileURLToPath(new URL('../cmd/ichor-ui/dist', import.meta.url)),
    emptyOutDir: true,
  },
})
