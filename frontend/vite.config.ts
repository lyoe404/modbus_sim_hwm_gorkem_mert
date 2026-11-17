import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
    proxy: {
  '/api': 'http://localhost:8080',
  '/metrics': 'http://localhost:8080',
  '/prometheus/api/v1/query': {
    target: 'http://localhost:9090',
    changeOrigin: true,
    rewrite: (path: string) =>
      path.replace(/^\/prometheus/, ''),
  },
},
  }
})
