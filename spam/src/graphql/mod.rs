/// graphql module — Phase 4 GraphQL API layer submodules.
///
/// Declared in order of dependency: types are the foundation; schema and resolvers
/// compose on top of them (schema/resolvers are wired in Plan 04-02).
///
/// Module layout follows the recommended project structure from 04-RESEARCH.md:
///   types.rs    — GraphQL output + input type definitions + DecodedEvent → Event mapping
///   schema.rs   — AppSchema type alias, build_schema() fn (Plan 04-02)
///   resolvers.rs — Query struct + #[Object] impl (Plan 04-02)
pub mod types;
