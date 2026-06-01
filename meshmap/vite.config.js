import { defineConfig, loadEnv } from 'vite'
import basicSsl from '@vitejs/plugin-basic-ssl'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const apiTarget = env.VITE_API_TARGET || 'http://127.0.0.1:8080'

  return {
    root: 'website',
    plugins: env.VITE_DEV_HTTPS === 'true' ? [basicSsl()] : [],
    server: {
      host: '0.0.0.0',
      port: 5178,
      strictPort: true,
      open: '/',
      watch: {
        usePolling: true,
      },
      proxy: {
        '/api': {
          target: apiTarget,
          changeOrigin: true,
        },
        '/nodes.json': {
          target: apiTarget,
          changeOrigin: true,
        },
      },
    },
  }
})
