/// <reference types="vitest/config" />
import { fileURLToPath, URL } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// In development, the API runs on its own port; the console calls it on the
// same address, like behind Caddy or any reverse proxy.
const api = process.env.DOCIBIS_API_URL ?? 'http://127.0.0.1:8080'
const proxy = Object.fromEntries(['/api', '/v1', '/mcp'].map((path) => [path, { target: api }]))

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: { proxy },
  preview: { proxy },
  // The console, and the pane of the Word add-in (/word/).
  build: {
    sourcemap: false,
    rolldownOptions: { input: { main: fileURLToPath(new URL('./index.html', import.meta.url)), word: fileURLToPath(new URL('./word/index.html', import.meta.url)) } },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
  },
})
