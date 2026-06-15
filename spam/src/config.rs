/// Configuration loader for lmdb2graphql.
///
/// Reads `~/deepfry/lmdb2graphql.yaml` per the DeepFry convention.
/// NEVER writes to or deletes `~/deepfry/` — tests must use `tempfile::tempdir()`.
use anyhow::Context;
use serde::Deserialize;
use std::io::IsTerminal;
use std::path::PathBuf;

/// Phase 1 minimal config.
///
/// Grows in later phases to carry GraphQL server settings, etc.
#[derive(Debug, Deserialize)]
pub struct Config {
    /// Path to the directory containing strfry's `data.mdb` (the LMDB env dir).
    pub strfry_db_path: PathBuf,

    /// LMDB map_size in bytes — must be >= strfry's configured `dbParams.mapsize`.
    /// Default: 10 TiB (10_995_116_277_760) matching `../config/strfry/strfry.conf`.
    #[serde(default = "default_map_size")]
    pub map_size: usize,

    /// Pinned strfry Docker image reference including full sha256 digest.
    /// Example: `"dockurr/strfry@sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5"`
    pub pinned_strfry_version: String,

    /// Pinned hoytech/strfry git commit SHA corresponding to the Docker image above.
    /// Example: `"f31a1b9df3a6da5fe96a9d61b5e80ed9b582f135"`
    pub pinned_strfry_commit: String,

    /// HTTP bind address for the GraphQL server. Default: 127.0.0.1:8080 (loopback).
    ///
    /// CR-01: Defaults to loopback so the unauthenticated full-DB GraphQL endpoint is
    /// never silently exposed on every interface. lmdb2graphql is an internal DeepFry
    /// sidecar (co-located with strfry); the Docker compose network publishes the port
    /// explicitly. Operators must opt into wider exposure by setting `bind_address` in
    /// YAML. main.rs emits a `tracing::warn!` when a non-loopback address is bound.
    ///
    /// Follows the `~/deepfry/lmdb2graphql.yaml` config convention.
    /// Override in YAML as `bind_address: "0.0.0.0:8080"` to widen exposure.
    #[serde(default = "default_bind_address")]
    pub bind_address: String,
}

/// Default map_size: 10 TiB — matches `../config/strfry/strfry.conf mapsize = 10995116277760`.
/// LMDB-04: map_size must be >= strfry's configured mapsize.
fn default_map_size() -> usize {
    10_995_116_277_760
}

/// Default bind_address for the GraphQL HTTP server.
///
/// CR-01: Binds loopback (127.0.0.1:8080) when omitted from YAML, forcing operators to
/// explicitly opt into wider exposure. The endpoint serves the entire strfry corpus with
/// no authentication, full introspection, and a GraphiQL playground — a default-public
/// (0.0.0.0) bind would expose all of that to any host that can route to the box. The
/// DeepFry deployment model co-locates this sidecar with strfry; the compose network maps
/// the published port explicitly.
fn default_bind_address() -> String {
    "127.0.0.1:8080".to_string()
}

/// Pinned strfry Docker image digest written into an auto-created config.
/// Mirrors `config/lmdb2graphql.yaml.example` (D-08/D-09).
const PINNED_STRFRY_VERSION: &str =
    "dockurr/strfry@sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5";

/// Pinned hoytech/strfry git commit corresponding to [`PINNED_STRFRY_VERSION`].
const PINNED_STRFRY_COMMIT: &str = "f31a1b9df3a6da5fe96a9d61b5e80ed9b582f135";

/// Load config from `~/deepfry/lmdb2graphql.yaml`.
///
/// # Errors
/// Returns an error if the home directory cannot be determined, the config file
/// cannot be read, or the YAML cannot be deserialized into [`Config`].
///
/// # Safety (CLAUDE.md)
/// This function NEVER writes to `~/deepfry/`. Tests must use a `tempfile::tempdir()`
/// and call [`load_from`] with the temp path instead.
pub fn load() -> anyhow::Result<Config> {
    let home = dirs::home_dir().context("cannot determine home directory")?;
    let path = home.join("deepfry").join("lmdb2graphql.yaml");

    // Auto-create on first run: if no config exists and we have an interactive
    // terminal, prompt the operator for the strfry DB path and write a config
    // with the pinned defaults. Never overwrites an existing file (CLAUDE.md).
    if !path.exists() && std::io::stdin().is_terminal() {
        prompt_and_create(&path)?;
    }

    load_from(&path)
}

/// Interactively prompt for the strfry DB path and write a new config file.
///
/// Only called when `path` does not already exist and stdin is a TTY. Writes the
/// pinned strfry version/commit defaults so the operator only supplies the DB path.
fn prompt_and_create(path: &std::path::Path) -> anyhow::Result<()> {
    use std::io::Write;

    println!("No config found at {}.", path.display());
    print!("Enter the path to strfry's LMDB directory (containing data.mdb): ");
    std::io::stdout().flush().context("flush prompt")?;

    let mut input = String::new();
    std::io::stdin()
        .read_line(&mut input)
        .context("read strfry DB path from stdin")?;
    let db_path = input.trim();
    anyhow::ensure!(!db_path.is_empty(), "strfry DB path must not be empty");

    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent)
            .with_context(|| format!("create config dir {}", parent.display()))?;
    }

    let contents = format!(
        "# lmdb2graphql.yaml — auto-generated on first run.\n\
         strfry_db_path: {db_path}\n\
         bind_address: \"{}\"\n\
         map_size: {}\n\
         pinned_strfry_version: \"{}\"\n\
         pinned_strfry_commit: \"{}\"\n",
        default_bind_address(),
        default_map_size(),
        PINNED_STRFRY_VERSION,
        PINNED_STRFRY_COMMIT,
    );
    std::fs::write(path, contents).with_context(|| format!("write config to {}", path.display()))?;
    println!("Wrote config to {}.", path.display());

    Ok(())
}

/// Load config from an explicit path.
///
/// Separated from [`load`] to allow tests to use a temp directory without
/// touching `~/deepfry/`.
pub fn load_from(path: &std::path::Path) -> anyhow::Result<Config> {
    let text = std::fs::read_to_string(path)
        .with_context(|| format!("read config from {}", path.display()))?;
    let cfg: Config =
        serde_yaml_ng::from_str(&text).with_context(|| format!("parse YAML {}", path.display()))?;
    Ok(cfg)
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Verify the map_size default is the 10 TiB value from strfry.conf (LMDB-04).
    #[test]
    fn test_map_size_default() {
        assert_eq!(
            default_map_size(),
            10_995_116_277_760,
            "default map_size must match strfry.conf mapsize (10 TiB)"
        );
    }

    /// Load a minimal config from a tempdir; assert map_size default and pin fields parse.
    /// NEVER touches ~/deepfry/ — uses tempfile::tempdir() per CLAUDE.md.
    #[test]
    fn test_load_from_tempdir() {
        let dir = tempfile::tempdir().expect("create tempdir");
        let config_path = dir.path().join("lmdb2graphql.yaml");

        let yaml = r#"
strfry_db_path: /app/strfry-db
pinned_strfry_version: "dockurr/strfry@sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5"
pinned_strfry_commit: "f31a1b9df3a6da5fe96a9d61b5e80ed9b582f135"
"#;
        std::fs::write(&config_path, yaml).expect("write test config");

        let cfg = load_from(&config_path).expect("load config");

        // map_size should default to 10 TiB (not specified in YAML above)
        assert_eq!(
            cfg.map_size,
            10_995_116_277_760,
            "map_size default must be 10 TiB"
        );
        assert_eq!(
            cfg.strfry_db_path,
            std::path::PathBuf::from("/app/strfry-db")
        );
        assert!(
            cfg.pinned_strfry_version
                .contains("sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5"),
            "pinned_strfry_version must contain the full digest"
        );
        assert_eq!(
            cfg.pinned_strfry_commit,
            "f31a1b9df3a6da5fe96a9d61b5e80ed9b582f135"
        );
    }

    /// Verify bind_address defaults to "127.0.0.1:8080" (loopback) when omitted from YAML.
    /// CR-01: loopback default prevents silent public exposure of the unauthenticated endpoint.
    /// NEVER touches ~/deepfry/ — uses tempfile::tempdir() per CLAUDE.md.
    #[test]
    fn test_bind_address_default() {
        let dir = tempfile::tempdir().expect("create tempdir");
        let config_path = dir.path().join("lmdb2graphql.yaml");

        // YAML omits bind_address — expect the default
        let yaml = r#"
strfry_db_path: /app/strfry-db
pinned_strfry_version: "dockurr/strfry@sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5"
pinned_strfry_commit: "f31a1b9df3a6da5fe96a9d61b5e80ed9b582f135"
"#;
        std::fs::write(&config_path, yaml).expect("write test config");

        let cfg = load_from(&config_path).expect("load config");
        assert_eq!(
            cfg.bind_address, "127.0.0.1:8080",
            "bind_address must default to 127.0.0.1:8080 (loopback) when omitted from YAML (CR-01)"
        );
    }

    /// Verify an explicit bind_address in YAML overrides the default.
    #[test]
    fn test_explicit_bind_address() {
        let dir = tempfile::tempdir().expect("create tempdir");
        let config_path = dir.path().join("lmdb2graphql.yaml");

        let yaml = r#"
strfry_db_path: /app/strfry-db
pinned_strfry_version: "dockurr/strfry@sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5"
pinned_strfry_commit: "f31a1b9df3a6da5fe96a9d61b5e80ed9b582f135"
bind_address: "127.0.0.1:9090"
"#;
        std::fs::write(&config_path, yaml).expect("write test config");

        let cfg = load_from(&config_path).expect("load config");
        assert_eq!(
            cfg.bind_address, "127.0.0.1:9090",
            "explicit bind_address must override the default"
        );
    }

    /// Verify explicit map_size overrides the default.
    #[test]
    fn test_explicit_map_size() {
        let dir = tempfile::tempdir().expect("create tempdir");
        let config_path = dir.path().join("lmdb2graphql.yaml");

        let yaml = r#"
strfry_db_path: /custom/db
map_size: 21990232555520
pinned_strfry_version: "dockurr/strfry@sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5"
pinned_strfry_commit: "f31a1b9df3a6da5fe96a9d61b5e80ed9b582f135"
"#;
        std::fs::write(&config_path, yaml).expect("write test config");

        let cfg = load_from(&config_path).expect("load config");
        assert_eq!(cfg.map_size, 21_990_232_555_520_usize, "explicit map_size must be honored");
    }
}
