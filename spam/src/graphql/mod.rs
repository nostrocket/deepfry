/// graphql module — Phase 4 GraphQL API layer submodules.
///
/// Module layout follows the recommended project structure from 04-RESEARCH.md:
///   types.rs    — GraphQL output + input type definitions + DecodedEvent → Event mapping
///   schema.rs   — AppSchema type alias, AppState, build_schema() fn (Plan 04-02)
///   resolvers.rs — Query struct + #[Object] impl (Plan 04-02)
pub mod resolvers;
pub mod schema;
pub mod types;
