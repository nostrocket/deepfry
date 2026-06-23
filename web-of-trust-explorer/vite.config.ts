import { defineConfig } from 'vite';

// Cross-origin isolation is REQUIRED for performance.measureUserAgentSpecificMemory()
// (the D-10 peak-heap verdict metric used by Plan 03). It needs both:
//   Cross-Origin-Opener-Policy: same-origin
//   Cross-Origin-Embedder-Policy: require-corp
// See 01-RESEARCH.md § Pitfall 4. Web Worker support is Vite-native (no extra config) —
// workers are spawned via `new Worker(new URL('./x.worker.ts', import.meta.url), { type: 'module' })`.
const crossOriginIsolationHeaders = {
  'Cross-Origin-Opener-Policy': 'same-origin',
  'Cross-Origin-Embedder-Policy': 'require-corp',
};

// The Go bridge serves the binary frame at GET /graph.bin on a different port.
// A direct cross-origin fetch would be BLOCKED by COEP require-corp unless the
// bridge sends CRP cross-origin headers. Instead we proxy /graph.bin SAME-ORIGIN
// through Vite (RESEARCH Pitfall 3 / Open Question 3 recommendation): the browser
// never makes a cross-origin request in dev, so COEP/CORP never applies and the
// cross-origin-isolation headers above stay intact for measureUserAgentSpecificMemory.
// The bridge's own CRP+CORS headers remain the documented production-style
// fallback but are not relied on here.
const BRIDGE_ORIGIN = 'http://127.0.0.1:8081';

export default defineConfig({
  server: {
    headers: crossOriginIsolationHeaders,
    proxy: {
      '/graph.bin': {
        target: BRIDGE_ORIGIN,
        changeOrigin: true,
      },
    },
  },
  preview: {
    headers: crossOriginIsolationHeaders,
  },
  worker: {
    format: 'es',
  },
});
