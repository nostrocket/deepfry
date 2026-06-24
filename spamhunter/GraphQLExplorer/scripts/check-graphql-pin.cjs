#!/usr/bin/env node
/*
 * graphql major-version pin guard (Pitfall 11).
 *
 * The typed-client toolchain (@graphql-codegen/client-preset@6) caps its
 * `graphql` peer at the v16 line. A newer major silently breaks codegen
 * output (peer warnings + empty/incorrect typed documents) while urql keeps
 * working at runtime, masking the problem. This guard reads the RESOLVED
 * installed version and fails the process whenever the installed major
 * exceeds the supported line, so a future `npm install` that floats graphql
 * forward trips loudly at install time rather than silently at codegen time.
 *
 * The check is a tripwire on the installed major, NOT an install verifier:
 * if graphql is not yet present (fresh checkout, before deps install) it
 * reports a clean status and exits 0.
 */

'use strict';

const MAX_SUPPORTED_MAJOR = 16;

function resolveInstalledVersion() {
  try {
    // require resolves through node_modules from this script's location.
    return require('graphql/package.json').version;
  } catch (_) {
    return null;
  }
}

const version = resolveInstalledVersion();

if (version === null) {
  console.log('PIN_OK: graphql not installed yet — run npm install (guard is a major-version tripwire, not an install check)');
  process.exit(0);
}

const major = Number.parseInt(String(version).split('.')[0], 10);

if (Number.isNaN(major)) {
  console.error(`PIN_FAIL: could not parse graphql version "${version}"`);
  process.exit(1);
}

if (major > MAX_SUPPORTED_MAJOR) {
  console.error(
    `PIN_FAIL: resolved graphql ${version} (major ${major}) exceeds the supported major ${MAX_SUPPORTED_MAJOR}.`
  );
  console.error(
    `The codegen client-preset toolchain does not support this major; exact-pin graphql to the ${MAX_SUPPORTED_MAJOR}.x line.`
  );
  process.exit(1);
}

console.log(`PIN_OK: graphql ${version} (major ${major}) within supported line (<= ${MAX_SUPPORTED_MAJOR})`);
process.exit(0);
