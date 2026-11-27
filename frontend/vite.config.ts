import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitest/config'
import type { Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// devPolicy loosens index.html's Content-Security-Policy on the dev server
// alone, which `wails dev` serves the page from: React's refresh adds an
// inline script there, and hot reload opens a websocket. The built page,
// which the app ships, keeps the policy as written.
const devPolicy: Plugin = {
  name: 'inkuna-dev-policy',
  apply: 'serve',
  transformIndexHtml(html) {
    return html
      .replace("script-src 'self'", "script-src 'self' 'unsafe-inline'")
      .replace("connect-src 'self'", "connect-src 'self' ws: wss:")
  },
}

// notices serves THIRD_PARTY_NOTICES, which internal/notices generates at
// the repository root, as the text of virtual:third-party-notices. The
// file is outside the frontend, where the dev server and the tests refuse
// to read a ?raw import from, and the page must carry it to ship it.
const noticesID = 'virtual:third-party-notices'
const noticesPath = fileURLToPath(new URL('../THIRD_PARTY_NOTICES', import.meta.url))
const notices: Plugin = {
  name: 'inkuna-third-party-notices',
  resolveId(id) {
    return id === noticesID ? '\0' + noticesID : undefined
  },
  load(id) {
    if (id !== '\0' + noticesID) return undefined
    this.addWatchFile(noticesPath)
    return `export default ${JSON.stringify(readFileSync(noticesPath, 'utf8'))}`
  },
}

export default defineConfig({
  plugins: [react(), tailwindcss(), devPolicy, notices],
  build: { outDir: '../ui/dist', emptyOutDir: true },
  test: { environment: 'jsdom', setupFiles: './src/test/setup.ts', css: true, globals: true },
})
