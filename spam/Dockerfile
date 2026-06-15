# syntax=docker/dockerfile:1
#
# lmdb2graphql — multi-stage Alpine Dockerfile (OPS-02)
#
# Stage 1 builds a statically-linked binary using the musl toolchain.
# Stage 2 is a minimal Alpine runtime image that carries only the binary.
#
# IMPORTANT: verify static linkage after any dependency change:
#   docker build --target builder -t lmdb2graphql-builder spam/
#   docker run --rm lmdb2graphql-builder \
#       ldd /src/target/x86_64-unknown-linux-musl/release/lmdb2graphql
# If "liblmdb.so.0" appears, add "ENV LMDB_STATIC=1" to the builder stage
# (RESEARCH.md Pitfall 3 / Open Question 1).

# ── Stage 1: builder ────────────────────────────────────────────────────────
FROM rust:alpine AS builder

# Build dependencies:
#   musl-dev  — musl C library headers required for crt-static linking
#   g++       — C++ toolchain needed by build.rs to compile reference/golpe_comparators.cpp
#   lmdb-dev  — provides lmdb.h at /usr/include/lmdb.h (build.rs probe path 4)
#   pkgconfig — used by build.rs pkg-config probe for lmdb.h
RUN apk add --no-cache \
    musl-dev \
    g++ \
    lmdb-dev \
    pkgconfig

WORKDIR /src
COPY . .

# CRITICAL: rust-toolchain.toml pins "stable-x86_64-apple-darwin" which is
# invalid on Linux. Remove it so rustup uses the stable Linux musl toolchain
# that ships with this image. Without this line the build fails with:
#   error: override file '/src/rust-toolchain.toml' specifies an unavailable
#   channel 'stable-x86_64-apple-darwin'
# (RESEARCH.md Pitfall 1)
RUN rm -f rust-toolchain.toml

# Produce a statically-linked binary. +crt-static links the C runtime statically
# so the runtime Alpine image needs no libc.so. The cc-compiled golpe_comparators.cpp
# shim (build.rs) also respects this flag.
ENV RUSTFLAGS="-C target-feature=+crt-static"
RUN cargo build --release --target x86_64-unknown-linux-musl

# ── Stage 2: minimal runtime ─────────────────────────────────────────────────
FROM alpine:3.21

# ca-certificates: needed if any future TLS calls are added (no-op overhead now).
# lmdb is NOT needed here — the binary is statically linked against libgolpe_comparators.a
# which embeds the golpe C++ comparators; lmdb-sys builds from vendored source by default.
RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=builder /src/target/x86_64-unknown-linux-musl/release/lmdb2graphql /app/lmdb2graphql

EXPOSE 8080

ENTRYPOINT ["/app/lmdb2graphql"]
