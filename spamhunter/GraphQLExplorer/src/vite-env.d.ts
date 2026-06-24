/// <reference types="vite/client" />

// Type the env contract once so the single config module (transport/config.ts)
// has a typed VITE_GRAPHQL_URL under `strict`, and a typo at any call site is
// caught at compile time (WR-02). Optional (`?`) reflects that it may be unset
// and fall back to the loopback default.
interface ImportMetaEnv {
  readonly VITE_GRAPHQL_URL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
