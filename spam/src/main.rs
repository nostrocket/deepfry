/// lmdb2graphql — startup gate
///
/// Sequential startup sequence (PATTERNS.md startup pattern):
///   1. Initialize tracing (JSON for Docker, pretty for dev)
///   2. Load config from ~/deepfry/lmdb2graphql.yaml
///   3. Open LMDB env (read-only, production path — NO_LOCK only in tests)
///   4. Read Meta + assert_db_version (LMDB-02) + assert_endianness (LMDB-03)
///   5. Log pinned_strfry_version alongside detected db_version (OPS-04 basic startup line)
///   6. Run comparator self-check (LMDB-06 / D-04)
///   7. On ANY Err → anyhow propagates to main → process exits non-zero (fail-closed, V7)
use anyhow::Context;
use lmdb2graphql::lmdb;

fn main() -> anyhow::Result<()> {
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

    Ok(())
}
