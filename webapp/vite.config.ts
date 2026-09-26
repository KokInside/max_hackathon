import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// В разработке API проксируется на локальный бэкенд.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: { '/api': process.env.API_URL ?? 'http://127.0.0.1:18080' },
  },
  build: { target: 'es2020', sourcemap: false },
})
