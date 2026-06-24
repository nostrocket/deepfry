import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Direct cross-origin connection to the lens over wildcard CORS — NO server.proxy.
// The base URL is configured via VITE_GRAPHQL_URL (see src/transport/config.ts), not here.
export default defineConfig({
  plugins: [react()],
})
