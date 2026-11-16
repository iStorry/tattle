import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [react()],
  // The Go binary embeds this directory.
  build: { outDir: '../internal/ui/dist', emptyOutDir: true },
  // During development, "pnpm dev" talks to a running "tattle --demo".
  server: { proxy: { '/api': 'http://localhost:8477' } },
})
