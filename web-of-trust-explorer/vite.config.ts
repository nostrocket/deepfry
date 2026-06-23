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

export default defineConfig({
  server: {
    headers: crossOriginIsolationHeaders,
  },
  preview: {
    headers: crossOriginIsolationHeaders,
  },
  worker: {
    format: 'es',
  },
});
