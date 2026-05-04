// Package envfile resolves the on-disk path to the quarantine LMDB,
// matching the precedence the deepfry compose stack already uses.
//
// Operators set STRFRY_QUARANTINE_DB_PATH in the project root's `.env`
// file (gitignored, see .env.example). docker-compose.strfry.yml expands
// it via `${STRFRY_QUARANTINE_DB_PATH:-./data/strfry-quarantine-db}`.
// We mirror that lookup so the rescuer doesn't introduce a parallel
// configuration surface.
package envfile

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// EnvVar is the variable both this resolver and docker-compose read.
	EnvVar = "STRFRY_QUARANTINE_DB_PATH"
	// DefaultPath matches the fallback in docker-compose.strfry.yml.
	DefaultPath = "./data/strfry-quarantine-db"
)

// Source describes where the resolved path came from. Useful for logs.
type Source string

const (
	SourceFlag    Source = "flag"
	SourceEnv     Source = "env"
	SourceEnvFile Source = "envfile"
	SourceDefault Source = "default"
)

// Resolve picks the LMDB path using this precedence:
//  1. flagValue if non-empty
//  2. STRFRY_QUARANTINE_DB_PATH from the process env
//  3. STRFRY_QUARANTINE_DB_PATH from the .env file at envFilePath
//  4. DefaultPath
//
// Relative paths are resolved against workingDir so they match what
// docker-compose would have used.
func Resolve(flagValue, envFilePath, workingDir string) (path string, src Source, err error) {
	if flagValue != "" {
		return absJoin(workingDir, flagValue), SourceFlag, nil
	}
	if v, ok := os.LookupEnv(EnvVar); ok && v != "" {
		return absJoin(workingDir, v), SourceEnv, nil
	}
	if envFilePath != "" {
		v, found, err := lookupInDotenv(envFilePath, EnvVar)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("read %s: %w", envFilePath, err)
		}
		if found && v != "" {
			return absJoin(workingDir, v), SourceEnvFile, nil
		}
	}
	return absJoin(workingDir, DefaultPath), SourceDefault, nil
}

// lookupInDotenv parses a minimal subset of .env syntax: KEY=value, with
// optional surrounding quotes, optional `export ` prefix, and `#` comments.
// Returns ("", false, nil) when the file exists but the key isn't there;
// returns os.ErrNotExist wrapped when the file is missing.
func lookupInDotenv(path, key string) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		if k != key {
			continue
		}
		v := strings.TrimSpace(line[eq+1:])
		// Strip a trailing inline comment if it follows a space.
		if hash := strings.Index(v, " #"); hash >= 0 {
			v = strings.TrimSpace(v[:hash])
		}
		// Strip matching surrounding quotes.
		if len(v) >= 2 {
			if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
				v = v[1 : len(v)-1]
			}
		}
		return v, true, nil
	}
	if err := scanner.Err(); err != nil {
		return "", false, err
	}
	return "", false, nil
}

// absJoin makes p absolute relative to workingDir if it isn't already.
// If workingDir is empty, the path is left as-is.
func absJoin(workingDir, p string) string {
	if filepath.IsAbs(p) || workingDir == "" {
		return p
	}
	return filepath.Join(workingDir, p)
}
