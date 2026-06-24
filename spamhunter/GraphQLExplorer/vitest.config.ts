import { defineConfig } from 'vitest/config'

// Transport unit tests run in Node — no DOM, no network. The classifier and
// readiness/paginate helpers are pure logic over hand-built fixtures.
export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
})
