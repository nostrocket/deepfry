/// query module — Phase 3 query engine submodules.
///
/// Declared in alphabetical order per project convention (mirrors src/lmdb/mod.rs).
///
/// Plans 03-01..03-04 add submodules progressively:
/// - filter: plan 03-01 (done)
/// - merge, router: plan 03-02 (this plan)
/// - hydrate: plan 03-03 (TODO)
/// - engine: plan 03-04 (TODO)
pub mod filter;
pub mod hydrate;
pub mod merge;
pub mod router;
