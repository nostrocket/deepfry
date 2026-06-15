/// lmdb2graphql — startup gate + GraphQL HTTP server
///
/// Bind-once / zero-gap startup sequence (OPS-01 gap-closure, CR-01/CR-02 fix):
///   1. Initialize tracing (JSON for Docker, pretty for dev)
///   2. Load config from ~/deepfry/lmdb2graphql.yaml
///   3. Create readiness flag (false) and empty schema cell
///   4. Bind TCP listener ONCE — serves the single router for the entire process lifetime
///      The listener is NEVER torn down or re-bound; no connection-refused gap (CR-01)
///   5. Spawn axum::serve on the single gated router — POST /graphql returns 503 until
///      the schema cell is populated; /health and /ready are always served (OPS-01)
///   6. Open LMDB env (read-only) — fail-closed via ? propagation
///   7. Read Meta + assert_db_version (LMDB-02) + assert_endianness (LMDB-03) — fail-closed
///   8. Run comparator self-check (LMDB-06 / D-04) — fail-closed
///   9. ONLY AFTER the self-check gate returns Ok:
///        - populate schema cell (schema_cell.set(schema))
///        - THEN store(true, Release) — /ready now returns 200, /graphql now executes queries
///  10. Await the serve task; surface any serve error or join error
///
/// Fail-closed (V7): any Err in steps 6–8 propagates via ? to main's anyhow::Result<()>
/// → process exits non-zero; the serve task is dropped; /ready never reaches 200.
///
/// Deviation from plan 05-03 (Rule 3): the plan's "probe-shutdown → re-bind" approach is
/// infeasible without the connection-refused gap (CR-01) it was meant to eliminate — you
/// cannot swap a running axum server's routes. This design mounts /graphql from the start
/// but returns 503 and executes no query until the schema cell is populated, which is
/// security-equivalent for T-05-05-SC (no corpus reachable while not ready) and the only
/// way to satisfy OPS-01's continuous-observability requirement.
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};

use anyhow::Context;
use lmdb2graphql::graphql::schema::{AppState, build_schema};
use lmdb2graphql::lmdb;
use lmdb2graphql::server::{AppRouterState, build_router};
use tokio::sync::OnceCell;

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
    //    Config load is cheap and cfg.bind_address is needed to bind the listener.
    let cfg = lmdb2graphql::config::load().context("load config from ~/deepfry/lmdb2graphql.yaml")?;
    tracing::info!(
        strfry_db_path = %cfg.strfry_db_path.display(),
        pinned_strfry_version = %cfg.pinned_strfry_version,
        "config loaded"
    );

    // 3. Create the readiness flag and schema cell BEFORE the gate chain.
    //    OPS-01 / T-05-04: flag initialized false; store(true) is called ONLY after
    //    the gate chain passes (source order enforced; acceptance criterion asserts).
    //    schema_cell: populated ONLY after gates pass, BEFORE store(true); POST /graphql
    //    returns 503 and executes no query while the cell is empty (T-05-05-SC).
    let ready = Arc::new(AtomicBool::new(false));
    let schema_cell: Arc<OnceCell<lmdb2graphql::graphql::schema::AppSchema>> =
        Arc::new(OnceCell::new());

    // 4. Bind the TCP listener ONCE before the gate chain runs.
    //    This is the ONLY bind — the listener is never torn down or re-bound.
    //    CR-01/CR-02 fix: a single bound socket serves continuously; no connection-refused
    //    gap between a probe shutdown and a re-bind.
    let listener = tokio::net::TcpListener::bind(&cfg.bind_address)
        .await
        .context("bind HTTP listener")?;

    let local_addr = listener.local_addr()?;

    // CR-01: this endpoint serves the entire strfry corpus with no authentication,
    // full introspection, and a GraphiQL playground. Binding a non-loopback address
    // exposes all of that to any host that can route to the box. Warn loudly so the
    // exposure is never silent — operators must consciously opt into wide binds.
    // NOTE: fires right after a successful bind, before the gate chain runs (NON-LOOPBACK).
    if !local_addr.ip().is_loopback() {
        tracing::warn!(
            addr = %local_addr,
            "GraphQL server bound to a NON-LOOPBACK address — the unauthenticated, \
             full-introspection endpoint is reachable from any host that can route here. \
             Bind 127.0.0.1 unless wider exposure is intentional (CR-01)."
        );
    }

    // 5. Build the single gated router and spawn axum::serve on the bound listener.
    //    The router serves for the entire process lifetime — no probe task, no shutdown
    //    handshake, no re-bind. /health and /ready are always served; POST /graphql returns
    //    503 until the schema cell is populated after the gate chain (OPS-01).
    let state = AppRouterState {
        ready: Arc::clone(&ready),
        schema: Arc::clone(&schema_cell),
    };
    let router = build_router(state);
    let serve = tokio::spawn(async move { axum::serve(listener, router).await });

    tracing::info!(
        addr = %local_addr,
        "HTTP surface listening (/health, /ready; /graphql gated until ready) — startup gates running"
    );

    // 6. Open LMDB env read-only (production: READ_ONLY only, no NO_LOCK).
    //    Any Err here propagates to main → process exits non-zero (fail-closed V7).
    //    The serve task is dropped on process exit; /ready never reaches 200.
    let env = lmdb::env::open_read_only_env(&cfg.strfry_db_path, cfg.map_size)
        .context("open strfry LMDB env")?;

    // 7. Read Meta and run the version + endianness gates (fail-closed).
    let meta = lmdb::meta::read_meta(&env).context("read Meta from strfry LMDB")?;

    lmdb::meta::assert_db_version(&meta).with_context(|| {
        format!(
            "dbVersion gate failed (pinned strfry: {})",
            cfg.pinned_strfry_version
        )
    })?;

    lmdb::meta::assert_endianness(&meta).context("endianness gate failed")?;

    // 7a. Log pinned strfry version alongside detected dbVersion (OPS-04 / D-15 basic line).
    tracing::info!(
        db_version = meta.db_version,
        pinned_strfry_version = %cfg.pinned_strfry_version,
        pinned_strfry_commit = %cfg.pinned_strfry_commit,
        "Meta gates passed — dbVersion verified"
    );

    // 8. Run comparator self-check (fail-closed: any mismatch → exit non-zero).
    let golden = lmdb::self_check::GoldenVectors::load_committed()
        .context("load committed golden vectors for self-check")?;

    lmdb::self_check::run_comparator_self_check(&env, &golden)
        .context("comparator self-check failed — LMDB indexes do not match golden vectors")?;

    tracing::info!(
        indexes_verified = lmdb::indexes::ALL_EVENT_INDEXES.len(),
        "comparator self-check passed — all Event__* indexes verified"
    );

    // 9. ONLY AFTER all gates pass: populate the schema cell, THEN flip the readiness flag.
    //    OPS-01 / T-05-04: populate-before-flip ensures a 200 /ready response always implies
    //    the schema is present and /graphql is queryable (no window where /ready=200 but
    //    /graphql still returns 503).
    //    Source-order acceptance criterion: store(true, ..) must appear after
    //    run_comparator_self_check in source (verified by awk).
    let dict_cache = Arc::new(lmdb::payload::DictCache::new());
    let app_state = AppState {
        env: env.clone(), // cheap refcount clone
        dict_cache,
        meta: meta.clone(),
        pinned_strfry_version: cfg.pinned_strfry_version.clone(),
    };
    let schema = build_schema(app_state);
    // set() returns Err if the cell is already populated — ignore (can only be called once).
    let _ = schema_cell.set(schema);

    ready.store(true, Ordering::Release);
    tracing::info!(addr = %local_addr, "startup gates passed — service is ready");

    // 10. Keep the process alive on the single server. Surface serve errors and join errors.
    //     WR-01/WR-02 fix: Results are no longer discarded — errors are propagated to main.
    match serve.await {
        Ok(Ok(())) => Ok(()),
        Ok(Err(e)) => Err(e).context("axum serve"),
        Err(e) => Err(anyhow::anyhow!(e)).context("serve task panicked"),
    }
}
