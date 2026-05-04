package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEnv(t *testing.T, dir, contents string) string {
	t.Helper()
	p := filepath.Join(dir, ".env")
	if err := os.WriteFile(p, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolve_FlagWins(t *testing.T) {
	t.Setenv(EnvVar, "/from/env")
	dir := t.TempDir()
	envFile := writeEnv(t, dir, "STRFRY_QUARANTINE_DB_PATH=/from/dotenv\n")

	got, src, err := Resolve("/from/flag", envFile, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/from/flag" || src != SourceFlag {
		t.Errorf("got=%s src=%s, want /from/flag flag", got, src)
	}
}

func TestResolve_EnvBeatsDotenv(t *testing.T) {
	t.Setenv(EnvVar, "/from/env")
	dir := t.TempDir()
	envFile := writeEnv(t, dir, "STRFRY_QUARANTINE_DB_PATH=/from/dotenv\n")

	got, src, err := Resolve("", envFile, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/from/env" || src != SourceEnv {
		t.Errorf("got=%s src=%s, want /from/env env", got, src)
	}
}

func TestResolve_DotenvWhenEnvUnset(t *testing.T) {
	t.Setenv(EnvVar, "")
	os.Unsetenv(EnvVar)
	dir := t.TempDir()
	envFile := writeEnv(t, dir, "STRFRY_QUARANTINE_DB_PATH=/from/dotenv\n")

	got, src, err := Resolve("", envFile, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/from/dotenv" || src != SourceEnvFile {
		t.Errorf("got=%s src=%s, want /from/dotenv envfile", got, src)
	}
}

func TestResolve_DefaultWhenNothingSet(t *testing.T) {
	os.Unsetenv(EnvVar)
	dir := t.TempDir() // no .env in here
	envFile := filepath.Join(dir, ".env")

	got, src, err := Resolve("", envFile, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, DefaultPath)
	if got != want || src != SourceDefault {
		t.Errorf("got=%s src=%s, want %s default", got, src, want)
	}
}

func TestResolve_RelativeFlagJoinsWorkingDir(t *testing.T) {
	os.Unsetenv(EnvVar)
	dir := t.TempDir()
	got, src, err := Resolve("relative/db", "", dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "relative/db")
	if got != want || src != SourceFlag {
		t.Errorf("got=%s, want %s", got, want)
	}
}

func TestResolve_AbsoluteFlagPreserved(t *testing.T) {
	got, _, err := Resolve("/abs/path", "", "/some/cwd")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/abs/path" {
		t.Errorf("got=%s, want /abs/path", got)
	}
}

func TestResolve_DotenvParsing(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"plain", "STRFRY_QUARANTINE_DB_PATH=/x/y\n", "/x/y"},
		{"export prefix", "export STRFRY_QUARANTINE_DB_PATH=/exp\n", "/exp"},
		{"double quotes", `STRFRY_QUARANTINE_DB_PATH="/q1"` + "\n", "/q1"},
		{"single quotes", `STRFRY_QUARANTINE_DB_PATH='/q2'` + "\n", "/q2"},
		{"trailing comment", "STRFRY_QUARANTINE_DB_PATH=/c # note\n", "/c"},
		{"surrounding blank lines", "\n\n# c\nSTRFRY_QUARANTINE_DB_PATH=/b\n\n", "/b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv(EnvVar)
			dir := t.TempDir()
			envFile := writeEnv(t, dir, tt.body)
			got, src, err := Resolve("", envFile, dir)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want || src != SourceEnvFile {
				t.Errorf("got=%s src=%s, want %s envfile", got, src, tt.want)
			}
		})
	}
}

func TestResolve_DotenvMissingIsNotAnError(t *testing.T) {
	os.Unsetenv(EnvVar)
	got, src, err := Resolve("", "/no/such/file/.env", "/cwd")
	if err != nil {
		t.Fatalf("missing .env should be silent, got err: %v", err)
	}
	if src != SourceDefault {
		t.Errorf("src=%s, want default", src)
	}
	want := filepath.Join("/cwd", DefaultPath)
	if got != want {
		t.Errorf("got=%s, want %s", got, want)
	}
}
