//! build.rs — compile golpe's vendored C++ comparators via the cc crate
//!
//! SPIKE A4 resolution: lmdb-master-sys (used by heed) does NOT emit DEP_LMDB_INCLUDE.
//! We use pkg-config / Homebrew path as fallback to find lmdb.h. On Alpine/CI,
//! the system lmdb.h from the `lmdb-dev` package is used (added to Docker build stage).
//!
//! SPIKE A7 resolution: mdb_cmp_memn is not in the public lmdb.h (internal LMDB symbol).
//! The inline equivalent is implemented directly in golpe_comparators.cpp.

fn main() {
    // Rerun only when these source files change
    println!("cargo:rerun-if-changed=reference/golpe_comparators.cpp");
    println!("cargo:rerun-if-changed=reference/lmdbxx/lmdb++.h");

    let mut build = cc::Build::new();
    build
        .cpp(true)
        .file("reference/golpe_comparators.cpp")
        .include("reference/lmdbxx") // for placeholder lmdb++.h (re-exports lmdb.h)
        .flag("-std=c++17") // lmdbxx requires C++17
        .flag("-fno-exceptions") // CRITICAL: prevent UB from throw across Rust FFI (RFC 2945)
        .flag("-fno-rtti") // reduce binary size; RTTI not needed for comparators
        .flag("-w"); // suppress warnings from vendored C++ code

    // SPIKE A4: lmdb.h include path resolution
    // Strategy: check multiple sources in priority order:
    //   1. DEP_LMDB_INCLUDE env var (if lmdb-sys emits it — currently does not)
    //   2. pkg-config output for lmdb
    //   3. Homebrew default paths (macOS dev)
    //   4. System default path (Linux CI — lmdb-dev installed in Dockerfile)

    // 1. DEP_LMDB_INCLUDE from lmdb-sys (currently not emitted, but future-proof)
    if let Ok(include_path) = std::env::var("DEP_LMDB_INCLUDE") {
        if !include_path.is_empty() {
            build.include(&include_path);
        }
    }

    // 2. pkg-config (most reliable on Linux CI and macOS with Homebrew)
    let pkg_config_include = std::process::Command::new("pkg-config")
        .args(["--cflags-only-I", "lmdb"])
        .output()
        .ok()
        .and_then(|o| {
            if o.status.success() {
                String::from_utf8(o.stdout).ok()
            } else {
                None
            }
        })
        .map(|flags| flags.trim().replace("-I", "").trim().to_string())
        .filter(|s| !s.is_empty());

    // Track whether any probe located a concrete lmdb.h. CR-02: a silently
    // unresolved header lets cc fall through to an arbitrary default whose
    // MDB_val layout we cannot vouch for. The safe wrappers now use named-member
    // init (order-independent), but we still surface an unresolved header loudly.
    let mut lmdb_h_located = false;

    if let Some(include) = pkg_config_include {
        if std::path::Path::new(&include).join("lmdb.h").exists() {
            lmdb_h_located = true;
        }
        build.include(&include);
    }

    // 3. Homebrew paths (macOS dev environment)
    for homebrew_path in &[
        "/opt/homebrew/include",        // ARM Mac (Apple Silicon)
        "/usr/local/include",            // Intel Mac
        "/opt/homebrew/opt/lmdb/include", // explicit lmdb formula
    ] {
        if std::path::Path::new(homebrew_path).join("lmdb.h").exists() {
            build.include(homebrew_path);
            lmdb_h_located = true;
            break;
        }
    }

    // 4. System default: /usr/include (Linux CI — lmdb.h from lmdb-dev package)
    // cc crate includes /usr/include by default on Linux; no explicit include needed.
    if std::path::Path::new("/usr/include/lmdb.h").exists() {
        lmdb_h_located = true;
    }

    // CR-02 / WR-04: don't compile against an unverifiable header silently.
    if !lmdb_h_located {
        println!(
            "cargo:warning=build.rs could not locate lmdb.h via DEP_LMDB_INCLUDE, pkg-config, \
             Homebrew, or /usr/include. The cc compile may pick up an unknown lmdb.h whose \
             MDB_val ABI is unverified. Install lmdb headers (e.g. `brew install lmdb` or \
             the `lmdb-dev` package) or set DEP_LMDB_INCLUDE."
        );
    }

    build.compile("golpe_comparators"); // → libgolpe_comparators.a linked into binary
}
