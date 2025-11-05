import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          // 基础代码分割：React 相关、图表库、通用第三方依赖
          if (id.indexOf('node_modules') !== -1) {
            if (id.indexOf('react') !== -1) return 'react-vendor'
            if (id.indexOf('recharts') !== -1) return 'chart-vendor'
            if (id.indexOf('zustand') !== -1 || id.indexOf('swr') !== -1 || id.indexOf('date-fns') !== -1) return 'data-vendor'
            return 'vendor'
          }
        },
      },
    },
  },
  server: {
    host: '0.0.0.0',
    port: 3000,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
})
