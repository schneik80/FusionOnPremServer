import { resolve } from 'node:path'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The build writes straight into the Go server's embed target. emptyOutDir is
// required because that dir lives outside the Vite project root and holds the
// committed index.html placeholder, which this build replaces.
//
// The /api proxy supports the "open Vite on :5173 directly" dev workflow; the
// alternative is running the Go binary with `-server -dev`, which reverse-
// proxies the UI to this dev server instead.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../server/webdist',
    emptyOutDir: true,
    // Two real HTML entry points: the SPA and the Fusion palette's minimal
    // chat page. embeddedHandler serves /embed.html by name (it's a real file
    // in webdist); everything else still falls back to index.html.
    rollupOptions: {
      input: {
        main: resolve(__dirname, 'index.html'),
        embed: resolve(__dirname, 'embed.html'),
      },
    },
    // Never inline .json assets as `data:` URLs. Vite inlines small assets by
    // default, but tldraw *fetches* its translation files — and the app's CSP
    // is `connect-src 'self'`, which blocks fetching a data: URL. A 4-byte
    // `{}` translation file was being inlined and then failing to load, with
    // the rejection surfacing inside React's commit phase. Emitting them as
    // real same-origin files keeps the strict CSP intact. Returning undefined
    // for everything else leaves Vite's default behaviour alone.
    assetsInlineLimit: (filePath: string) => (filePath.endsWith('.json') ? false : undefined),
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
