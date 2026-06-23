import { defineConfig } from 'vitest/config';

// Vitest shares the same config surface as Vite (01-RESEARCH.md § Validation Architecture).
// The CPU data-pipeline tests are pure-function units (remap, generator, parse, transport,
// precision) — no DOM/GPU. Node environment is sufficient and fastest.
//
// Tests live both under tests/ and co-located in src/ (e.g. the binary-frame
// decode test next to GoBridgeTransport.ts, which mirrors the Go wire encoder).
export default defineConfig({
  test: {
    globals: true,
    environment: 'node',
    include: ['tests/**/*.test.ts', 'src/**/*.test.ts'],
  },
});
