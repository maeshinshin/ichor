import preact from '@preact/preset-vite'
import tailwindcss from '@tailwindcss/vite'
import { readFileSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    preact(),
    tailwindcss(),
    {
      // Serves the repository's THIRD-PARTY-NOTICES at /THIRD-PARTY-NOTICES.txt of the built UI.
      name: 'third-party-notices',
      generateBundle() {
        const source = readFileSync(fileURLToPath(new URL('../THIRD-PARTY-NOTICES', import.meta.url)), 'utf8')
        this.emitFile({ type: 'asset', fileName: 'THIRD-PARTY-NOTICES.txt', source })
      },
    },
  ],
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
