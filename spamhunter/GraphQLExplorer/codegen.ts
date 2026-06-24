import type { CodegenConfig } from '@graphql-codegen/cli'

// Codegen runs in Node — no browser, no CORS, ever — and only GENERATES types;
// the runtime urql client (src/transport/client.ts) talks to the live lens.
//
// Schema source = the checked-in SDL (schema.graphql), transcribed verbatim
// from contract.md §4. We do NOT introspect the live endpoint at codegen time:
// the lens enforces a query depth limit of 12 (contract §12), which rejects
// graphql-codegen's deep introspection query ("Query is nested too deep"). The
// SDL is the documented fallback per RESEARCH § "Environment Availability" and
// keeps codegen working regardless of backend availability. The runtime base
// URL stays a single env-configured source of truth in transport/config.ts
// (VITE_GRAPHQL_URL); codegen needs no URL because it reads the SDL.
const config: CodegenConfig = {
  schema: './schema.graphql',
  documents: ['src/**/*.{ts,tsx}'],
  ignoreNoDocuments: true,
  generates: {
    './src/gql/': {
      preset: 'client',
      // Emit type-only imports so the generated output satisfies the app's
      // `verbatimModuleSyntax: true` tsconfig (TS1484 otherwise).
      config: { useTypeImports: true },
    },
  },
}

export default config
