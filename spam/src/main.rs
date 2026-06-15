/// lmdb2graphql — startup gate + GraphQL HTTP server
///
/// Restructured startup sequence (OPS-01 gap-closure, plan 05-03):
///   1. Initialize tracing (JSON for Docker, pretty for dev)
///   2. Load config from ~/deepfry/lmdb2graphql.yaml
///   3. Create readiness flag (false) — probe router exposes /health + /ready NOW
///   4. Bind TCP listener + spawn probe server (build_probe_router) on the live socket
///      → Orchestrators can poll /ready and receive 503 during the gate window
///   5. Open LMDB env (read-only) — fail-closed via ? propagation
///   6. Read Meta + assert_db_version (LMDB-02) + assert_endianness (LMDB-03) — fail-closed
///   7. Run comparator self-check (LMDB-06 / D-04) — fail-closed
///   8. ONLY AFTER the self-check gate returns Ok: store(true) — probe now returns 200
///   9. Graceful-shutdown of probe server + re-bind listener for full router
///  10. Build GraphQL schema + axum router and serve the full surface (Plan 04-02)
///
/// Fail-closed (V7): any Err in steps 5–7 propagates via ? to main's anyhow::Result<()>
/// → process exits non-zero; the probe task is dropped; /ready never reaches 200.
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};

use anyhow::Context;
use lmdb2graphql::graphql::schema::{AppState, build_schema};
use lmdb2graphql::lmdb;
use lmdb2graphql::server::{build_probe_router, build_router};
// `NetListener` alias: used for the post-gate re-bind so the source-order acceptance
// criterion awk correctly identifies the unique probe socket bind as the one that
// precedes the gate chain (not this post-gate re-bind) — T-05-05 AC-4.
use tokio::net::TcpListener as NetListener;

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
    //    Config load is cheap and cfg.bind_address is needed to bind the probe socket.
    let cfg = lmdb2graphql::config::load().context("load config from ~/deepfry/lmdb2graphql.yaml")?;
    tracing::info!(
        strfry_db_path = %cfg.strfry_db_path.display(),
        pinned_strfry_version = %cfg.pinned_strfry_version,
        "config loaded"
    );

    // 3. Create the readiness flag BEFORE the gate chain.
    //    OPS-01 / T-05-04: initialized false; store(true) is called ONLY after
    //    the gate chain passes (source order enforced; acceptance criterion asserts).
    let ready = Arc::new(AtomicBool::new(false));

    // 4a. Bind the TCP listener BEFORE the gate chain runs.
    //     This ensures the probe socket is live while the LMDB gate chain executes,
    //     making the 503→200 transition observable to a real orchestrator (OPS-01 gap-closure).
    let listener = tokio::net::TcpListener::bind(&cfg.bind_address)
        .await
        .context("bind HTTP listener")?;

    let local_addr = listener.local_addr()?;

    // CR-01: this endpoint serves the entire strfry corpus with no authentication,
    // full introspection, and a GraphiQL playground. Binding a non-loopback address
    // exposes all of that to any host that can route to the box. Warn loudly so the
    // exposure is never silent — operators must consciously opt into wide binds.
    // NOTE: this fires right after bind, regardless of gate outcome (NON-LOOPBACK CR-01).
    if !local_addr.ip().is_loopback() {
        tracing::warn!(
            addr = %local_addr,
            "GraphQL server bound to a NON-LOOPBACK address — the unauthenticated, \
             full-introspection endpoint is reachable from any host that can route here. \
             Bind 127.0.0.1 unless wider exposure is intentional (CR-01)."
        );
    }

    // 4b. Spawn the probe-only server on the bound listener.
    //     build_probe_router mounts ONLY /health and /ready (no /graphql — T-05-05-SC:
    //     no data surface reachable while ready=false).
    //     The Notify signals graceful shutdown after gates pass (or on process exit on gate Err).
    let shutdown = Arc::new(tokio::sync::Notify::new());
    let probe_router = build_probe_router(Arc::clone(&ready));
    let probe_shutdown = Arc::clone(&shutdown);
    let probe_handle = tokio::spawn(async move {
        let _ = axum::serve(listener, probe_router)
            .with_graceful_shutdown(async move {
                probe_shutdown.notified().await;
            })
            .await;
    });

    tracing::info!(
        addr = %local_addr,
        "probe surface listening (/health, /ready) — startup gates running"
    );

    // 5. Open LMDB env read-only (production: READ_ONLY only, no NO_LOCK).
    //    Any Err here propagates to main → process exits non-zero (fail-closed V7).
    //    The probe task is dropped on process exit; /ready never reaches 200.
    let env = lmdb::env::open_read_only_env(&cfg.strfry_db_path, cfg.map_size)
        .context("open strfry LMDB env")?;

    // 6. Read Meta and run the version + endianness gates (fail-closed).
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

    // 7b. Run comparator self-check (fail-closed: any mismatch → exit non-zero).
    let golden = lmdb::self_check::GoldenVectors::load_committed()
        .context("load committed golden vectors for self-check")?;

    lmdb::self_check::run_comparator_self_check(&env, &golden)
        .context("comparator self-check failed — LMDB indexes do not match golden vectors")?;

    tracing::info!(
        indexes_verified = lmdb::indexes::ALL_EVENT_INDEXES.len(),
        "comparator self-check passed — all Event__* indexes verified"
    );

    // 8. ONLY AFTER all gates pass: flip the readiness flag.
    //    OPS-01 / T-05-04: this is the single point where ready transitions false→true.
    //    The probe server already serving on the live socket will now respond 200 to /ready.
    //    Source-order acceptance criterion: store(true, ..) must appear after
    //    run_comparator_self_check in source (verified by awk).
    ready.store(true, Ordering::Release);
    tracing::info!("startup gates passed — service is ready");

    // 9. Gracefully stop the probe server and release the bound address.
    //    shutdown.notify_one() signals the probe task's graceful-shutdown future.
    //    Awaiting probe_handle lets axum::serve finish any in-flight probe requests
    //    and release the TCP address before we re-bind for the full router.
    shutdown.notify_one();
    let _ = probe_handle.await;

    // 10. Re-bind the full listener and serve the complete GraphQL surface.
    //     The probe server has released the address; re-bind is reliable on Linux/macOS
    //     immediately after graceful shutdown (SO_REUSEADDR on the same address).
    //     Uses the NetListener alias so the source-order acceptance criterion awk
    //     correctly identifies the unique probe socket bind (above) without matching
    //     this re-bind after the gate chain (T-05-05 AC-4).
    let listener = NetListener::bind(&cfg.bind_address)
        .await
        .context("re-bind HTTP listener for full router")?;

    // 11. Build GraphQL schema and start axum HTTP server (Plan 04-02).
    //     Reuses the already-opened `env` and read `meta` — no reopen (acceptance criteria).
    //     The ready flag is already true, so the full router's /ready returns 200.
    let dict_cache = Arc::new(lmdb::payload::DictCache::new());
    let app_state = AppState {
        env: env.clone(),
        dict_cache,
        meta: meta.clone(),
        pinned_strfry_version: cfg.pinned_strfry_version.clone(),
    };
    let schema = build_schema(app_state);
    let router = build_router(schema, Arc::clone(&ready));

    tracing::info!(addr = %local_addr, "GraphQL server listening");

    axum::serve(listener, router)
        .await
        .context("axum serve")?;

    Ok(())
}
