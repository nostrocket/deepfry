//! The generic GraphQL-over-HTTP response envelope (contract §3 `{data, errors}`).
//!
//! A single generic [`GraphQlResponse<T>`] is the only shape any query decodes
//! into, so in-body `errors[]` can never be silently dropped: the client checks
//! `errors` BEFORE trusting `data` (D-08, contract §7 "always check json.errors
//! even on 200"). `extensions.code` carries the machine-actionable error code
//! (`INVALID_CURSOR` / `TOO_MANY_AUTHORS`; absent for internal/validation errors).
//!
//! Hand-written per D-10 (no introspection codegen). Mirrors the serde-derive
//! idiom in `crate::model` (`Option<T>` for nullable fields).

use serde::Deserialize;

/// The top-level GraphQL response: a `data` object and/or an `errors` array.
///
/// Both `data` and `errors` are optional so every spec-legal shape parses:
/// `{data}` (success), `{data:null, errors}` (resolver/internal error), and
/// `{data, errors}` (partial). `#[serde(default)]` on `errors` lets a body that
/// omits the key entirely (the common success case) deserialize without error.
#[derive(Deserialize, Debug)]
pub struct GraphQlResponse<T> {
    pub data: Option<T>,
    #[serde(default)]
    pub errors: Option<Vec<GraphQlError>>,
}

/// One entry in the GraphQL `errors[]` array.
///
/// `message` is always present (human-readable); `extensions` is optional and,
/// when present, may carry a machine-actionable `code`.
#[derive(Deserialize, Debug)]
pub struct GraphQlError {
    pub message: String,
    #[serde(default)]
    pub extensions: Option<Extensions>,
}

/// The `extensions` object on a GraphQL error.
///
/// `code` is the machine-actionable error code the loop branches on
/// (`INVALID_CURSOR`, `TOO_MANY_AUTHORS`); `None` for internal/validation errors
/// whose detail is deliberately not leaked (contract §7).
#[derive(Deserialize, Debug)]
pub struct Extensions {
    pub code: Option<String>,
}
