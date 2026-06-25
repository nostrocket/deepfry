//! Minimal `--resume` entry point for the connectivity-proving vertical slice (D-12).
//!
//! This is intentionally NOT the full Phase-5 clap surface (`run`/`export`): it
//! parses a single `--resume` bool, resolves the adapter endpoint from
//! `LMDB2GRAPHQL_URL` (default loopback per contract §10), opens the store, and
//! drives `enumerate::run` to enumerate every distinct pubkey into SQLite,
//! resumably. `store.close()` flushes the writer so every pubkey + the final
//! cursor/done update is durable before exit.

use std::path::Path;

/// On-disk store path for Phase 2. A config-driven path is OPS-03 / Phase-5
/// territory (D-12); a constant is sufficient for the vertical slice.
const DB_PATH: &str = "spamhunter.sqlite";

/// Default adapter endpoint: plain loopback HTTP (contract §10). Overridden by
/// the operator-supplied `LMDB2GRAPHQL_URL` (A2; not user input — see T-02-10).
const DEFAULT_ENDPOINT: &str = "http://127.0.0.1:8080/graphql";

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Single flag, no clap (D-12): clap's `run`/`export` surface is Phase 5.
    let resume = std::env::args().any(|a| a == "--resume");

    // Operator-supplied endpoint; default loopback (A2 / contract §10).
    let endpoint =
        std::env::var("LMDB2GRAPHQL_URL").unwrap_or_else(|_| DEFAULT_ENDPOINT.to_string());

    eprintln!(
        "enumerate: starting (resume={resume}) against {endpoint} -> {DB_PATH}"
    );

    let store = pubkey_iterator::store::Store::open(Path::new(DB_PATH))?;
    let client = pubkey_iterator::graphql::GraphQlClient::new(endpoint);

    pubkey_iterator::enumerate::run(&store, &client, resume).await?;

    // Flush the writer + final cursor/done update durably before exit.
    store.close()?;
    Ok(())
}
