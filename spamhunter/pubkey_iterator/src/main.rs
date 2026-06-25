//! The Phase-5 `clap` CLI surface (D-01/D-02): `run` and `export` subcommands
//! plus global `--config`/`--db` options.
//!
//! `run` drives a full end-to-end batch (enumerate → fetch → score → persist) on
//! one canonical run_id via [`pubkey_iterator::run::run_batch`], building a tokio
//! runtime only inside the `Run` arm (so `export` — a pure-SQLite op, filled in by
//! Plan 03 — stays synchronous). The default config path resolves to
//! `~/deepfry/pubkey_iterator_config.toml` via `$HOME` (no `dirs` dep, RESEARCH
//! A2); `--config` overrides it. Operator-supplied `--config`/`--db` paths are
//! trusted (local single-operator CLI, T-05-04 / V5).

use std::path::PathBuf;
use std::sync::Arc;

use clap::{Parser, Subcommand};

/// `pubkey_iterator` — the Nostr pubkey spam classifier.
#[derive(Parser, Debug)]
#[command(version, about, long_about = None)]
struct Cli {
    /// Path to the TOML config (default: ~/deepfry/pubkey_iterator_config.toml).
    #[arg(long, global = true)]
    config: Option<PathBuf>,

    /// Path to the SQLite store.
    #[arg(long, global = true, default_value = "spamhunter.sqlite")]
    db: PathBuf,

    #[command(subcommand)]
    command: Commands,
}

#[derive(Subcommand, Debug)]
enum Commands {
    /// Run a full batch: enumerate → fetch → score → persist.
    Run {
        /// Resume the latest unfinished run from its stored cursor.
        #[arg(long)]
        resume: bool,
    },
    /// Materialize the suspected-spammer snapshot for a run into SQLite.
    Export {
        /// Which run to export (default: the latest completed run).
        #[arg(long)]
        run_id: Option<i64>,
    },
}

/// Resolve the default config path: `$HOME/deepfry/pubkey_iterator_config.toml`
/// (RESEARCH A2 — no `dirs` dep). Falls back to a bare relative filename when
/// `$HOME` is unset (the operator can always pass `--config`).
fn default_config_path() -> PathBuf {
    match std::env::var_os("HOME") {
        Some(home) => PathBuf::from(home)
            .join("deepfry")
            .join("pubkey_iterator_config.toml"),
        None => PathBuf::from("pubkey_iterator_config.toml"),
    }
}

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let cli = Cli::parse();
    let config_path = cli.config.clone().unwrap_or_else(default_config_path);

    match cli.command {
        Commands::Run { resume } => {
            // Load the operator config (τ/bias/layers + adapter/whitelist URLs) and
            // open the store. The CLI reads config.adapter_url now — no env override
            // in the binary path (env overrides survive only in the live tests, A4).
            let config = pubkey_iterator::config::load(&config_path)?;
            let store = Arc::new(pubkey_iterator::store::Store::open(&cli.db)?);

            // Build the tokio runtime only here (export stays sync). run_batch takes
            // the sole Arc<Store> and owns the close()/mark_run_done lifecycle.
            let rt = tokio::runtime::Runtime::new()?;
            let run_id = rt.block_on(pubkey_iterator::run::run_batch(store, &config, resume))?;
            eprintln!("run complete: run_id={run_id}");
        }
        Commands::Export { run_id } => {
            // Plan 03 fills this arm (the suspected_spammer INSERT…SELECT). Until
            // then it is a clearly-marked placeholder that exits non-zero so a
            // premature `export` is loud, never a silent no-op.
            let _ = run_id;
            eprintln!("export: not yet implemented (Plan 05-03 fills this arm)");
            std::process::exit(2);
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    /// OPS-01 (CLI parse): clap parses `run --resume` → `Run { resume: true }`,
    /// `export --run-id 7` → `Export { run_id: Some(7) }`, and accepts the global
    /// `--config`/`--db` args BEFORE and AFTER the subcommand (`global = true`).
    #[test]
    fn parses_subcommands() {
        // `run --resume`.
        let cli = Cli::try_parse_from(["pubkey_iterator", "run", "--resume"])
            .expect("parse run --resume");
        match cli.command {
            Commands::Run { resume } => assert!(resume, "--resume → resume=true"),
            other => panic!("expected Run, got {other:?}"),
        }

        // `run` without --resume → resume=false.
        let cli = Cli::try_parse_from(["pubkey_iterator", "run"]).expect("parse run");
        match cli.command {
            Commands::Run { resume } => assert!(!resume, "no --resume → resume=false"),
            other => panic!("expected Run, got {other:?}"),
        }

        // `export --run-id 7`.
        let cli = Cli::try_parse_from(["pubkey_iterator", "export", "--run-id", "7"])
            .expect("parse export --run-id 7");
        match cli.command {
            Commands::Export { run_id } => assert_eq!(run_id, Some(7), "--run-id 7 → Some(7)"),
            other => panic!("expected Export, got {other:?}"),
        }

        // `export` without --run-id → None (defaults to latest done run at dispatch).
        let cli = Cli::try_parse_from(["pubkey_iterator", "export"]).expect("parse export");
        match cli.command {
            Commands::Export { run_id } => assert_eq!(run_id, None, "no --run-id → None"),
            other => panic!("expected Export, got {other:?}"),
        }

        // Global args BEFORE the subcommand.
        let cli = Cli::try_parse_from([
            "pubkey_iterator",
            "--config",
            "/tmp/cfg.toml",
            "--db",
            "/tmp/x.sqlite",
            "run",
        ])
        .expect("globals before subcommand");
        assert_eq!(cli.config.as_deref(), Some(std::path::Path::new("/tmp/cfg.toml")));
        assert_eq!(cli.db, std::path::PathBuf::from("/tmp/x.sqlite"));
        assert!(matches!(cli.command, Commands::Run { .. }));

        // Global args AFTER the subcommand (global = true).
        let cli = Cli::try_parse_from([
            "pubkey_iterator",
            "run",
            "--resume",
            "--config",
            "/tmp/cfg2.toml",
            "--db",
            "/tmp/y.sqlite",
        ])
        .expect("globals after subcommand");
        assert_eq!(cli.config.as_deref(), Some(std::path::Path::new("/tmp/cfg2.toml")));
        assert_eq!(cli.db, std::path::PathBuf::from("/tmp/y.sqlite"));
        match cli.command {
            Commands::Run { resume } => assert!(resume, "--resume still parsed alongside globals"),
            other => panic!("expected Run, got {other:?}"),
        }
    }

    /// `default_config_path` joins `$HOME/deepfry/pubkey_iterator_config.toml`
    /// when HOME is set (RESEARCH A2). Asserted via the path tail, not the literal
    /// HOME (which varies per environment).
    #[test]
    fn default_config_path_uses_home() {
        if std::env::var_os("HOME").is_some() {
            let p = default_config_path();
            assert!(
                p.ends_with("deepfry/pubkey_iterator_config.toml"),
                "default config path tail is deepfry/pubkey_iterator_config.toml (got {p:?})"
            );
        }
    }
}
