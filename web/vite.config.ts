import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/search': 'http://localhost:8080',
      '/chat': 'http://localhost:8080',
      '/health': 'http://localhost:8080',
      '/sources': 'http://localhost:8080',
      '/documents': 'http://localhost:8080',
      '/eval': 'http://localhost:8080',
    },
  },
  build: {
    outDir: '../internal/ui/dist',
    emptyOutDir: true,
  },
})
