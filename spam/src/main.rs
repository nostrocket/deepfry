/// lmdb2graphql — startup gate + GraphQL HTTP server
///
/// Sequential startup sequence (PATTERNS.md startup pattern):
///   1. Initialize tracing (JSON for Docker, pretty for dev)
///   2. Load config from ~/deepfry/lmdb2graphql.yaml
///   3. Open LMDB env (read-only, production path — NO_LOCK only in tests)
///   4. Read Meta + assert_db_version (LMDB-02) + assert_endianness (LMDB-03)
///   5. Log pinned_strfry_version alongside detected db_version (OPS-04 basic startup line)
///   6. Run comparator self-check (LMDB-06 / D-04)
///   7. On ANY Err → anyhow propagates to main → process exits non-zero (fail-closed, V7)
///   8. Build GraphQL schema + axum router and start serving (Plan 04-02)
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};

use anyhow::Context;
use lmdb2graphql::graphql::schema::{AppState, build_schema};
use lmdb2graphql::lmdb;
use lmdb2graphql::server::build_router;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    // 1. Initialize tracing: JSON output for Docker, pretty for dev.
    //    Controlled by RUST_LOG env var (e.g. RUST_LOG=info,lmdb2graphql=debug).
    tracing_subscriber::fmt()
        .json()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .with_writer(std::io::stderr)
        .init();

    tracing::info!(
        version = env!("CARGO_PKG_VERSION"),
        "lmdb2graphql starting"
    );

    // 2. Load config from ~/deepfry/lmdb2graphql.yaml.
    let cfg = lmdb2graphql::config::load().context("load config from ~/deepfry/lmdb2graphql.yaml")?;
    tracing::info!(
        strfry_db_path = %cfg.strfry_db_path.display(),
        pinned_strfry_version = %cfg.pinned_strfry_version,
        "config loaded"
    );

    // 3. Open LMDB env read-only (production: READ_ONLY only, no NO_LOCK).
    let env = lmdb::env::open_read_only_env(&cfg.strfry_db_path, cfg.map_size)
        .context("open strfry LMDB env")?;

    // 4. Read Meta and run the version + endianness gates (fail-closed).
    let meta = lmdb::meta::read_meta(&env).context("read Meta from strfry LMDB")?;

    lmdb::meta::assert_db_version(&meta).with_context(|| {
        format!(
            "dbVersion gate failed (pinned strfry: {})",
            cfg.pinned_strfry_version
        )
    })?;

    lmdb::meta::assert_endianness(&meta).context("endianness gate failed")?;

    // 5. Log pinned strfry version alongside detected dbVersion (OPS-04 / D-15 basic line).
    tracing::info!(
        db_version = meta.db_version,
        pinned_strfry_version = %cfg.pinned_strfry_version,
        pinned_strfry_commit = %cfg.pinned_strfry_commit,
        "Meta gates passed — dbVersion verified"
    );

    // 6. Run comparator self-check (fail-closed: any mismatch → exit non-zero).
    let golden = lmdb::self_check::GoldenVectors::load_committed()
        .context("load committed golden vectors for self-check")?;

    lmdb::self_check::run_comparator_self_check(&env, &golden)
        .context("comparator self-check failed — LMDB indexes do not match golden vectors")?;

    tracing::info!(
        indexes_verified = lmdb::indexes::ALL_EVENT_INDEXES.len(),
        "comparator self-check passed — all Event__* indexes verified"
    );

    // OPS-01 / T-05-04: readiness flag — initialized false; set true only after all startup
    // gates pass. main.rs is the authoritative gate chain; the flag is never set true before
    // run_comparator_self_check returns Ok (source order enforced; acceptance criterion asserts).
    let ready = Arc::new(AtomicBool::new(false));
    ready.store(true, Ordering::Release);
    tracing::info!("startup gates passed — service is ready");

    // 8. Build GraphQL schema and start axum HTTP server (Plan 04-02).
    //    Reuses the already-opened `env` and read `meta` — no reopen (acceptance criteria).
    let dict_cache = Arc::new(lmdb::payload::DictCache::new());
    let app_state = AppState {
        env: env.clone(),
        dict_cache,
        meta: meta.clone(),
        pinned_strfry_version: cfg.pinned_strfry_version.clone(),
    };
    let schema = build_schema(app_state);
    let router = build_router(schema, Arc::clone(&ready));

    // Bind address from config (bind_address; default 127.0.0.1:8080 loopback — CR-01).
    let listener = tokio::net::TcpListener::bind(&cfg.bind_address)
        .await
        .context("bind HTTP listener")?;

    let local_addr = listener.local_addr()?;

    // CR-01: this endpoint serves the entire strfry corpus with no authentication,
    // full introspection, and a GraphiQL playground. Binding a non-loopback address
    // exposes all of that to any host that can route to the box. Warn loudly so the
    // exposure is never silent — operators must consciously opt into wide binds.
    if !local_addr.ip().is_loopback() {
        tracing::warn!(
            addr = %local_addr,
            "GraphQL server bound to a NON-LOOPBACK address — the unauthenticated, \
             full-introspection endpoint is reachable from any host that can route here. \
             Bind 127.0.0.1 unless wider exposure is intentional (CR-01)."
        );
    }

    tracing::info!(addr = %local_addr, "GraphQL server listening");

    axum::serve(listener, router)
        .await
        .context("axum serve")?;

    Ok(())
}
