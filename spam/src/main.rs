mod lmdb;

fn main() {
    // Initialize tracing: JSON output for Docker, pretty for dev
    // (full startup gate wired in plan 03 — config + meta gates + self-check)
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .with_writer(std::io::stderr)
        .init();

    tracing::info!(
        version = env!("CARGO_PKG_VERSION"),
        "lmdb2graphql starting"
    );
}
