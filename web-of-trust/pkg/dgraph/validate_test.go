package dgraph

import "testing"

// TestValidatePubkey covers every garbage type confirmed in the live DB
// (CONTEXT.md §"Real garbage nodes") plus a valid lowercase 64-char hex pubkey.
// No build tag: runs under `make test` / `go test -short`.
func TestValidatePubkey(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "valid lowercase 64-char hex",
			input:   "e88a691e98da9e73cfc6a93bd2e6c9a3ff5df66ea71abb95f5e2b1b5e5e7e3f1",
			wantErr: false,
		},
		{
			name:    "uppercase 64-char hex",
			input:   "83E818DFED1B3A9DBBD3EBE4AE1FC0EB9614DB3B4C6B1765BF1E3B9DFE0C5F2A",
			wantErr: true,
		},
		{
			name:    "mixed-case 64-char hex",
			input:   "83e818DFed1b3A9dbbd3EBe4ae1fc0eb9614db3b4c6b1765bf1e3b9dfe0c5f2a",
			wantErr: true,
		},
		{
			name:    "short hex: f1",
			input:   "f1",
			wantErr: true,
		},
		{
			name:    "short hex: cbdc",
			input:   "cbdc",
			wantErr: true,
		},
		{
			name:    "short hex: de",
			input:   "de",
			wantErr: true,
		},
		{
			name:    "relay-URL blob (114-char garbage)",
			input:   "0115wss://relay.mostr.pub/did:plc:user-0000000000000000000000000000000000000000000000000000000000000000",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "63-char hex (one short)",
			input:   "e88a691e98da9e73cfc6a93bd2e6c9a3ff5df66ea71abb95f5e2b1b5e5e7e3f",
			wantErr: true,
		},
		{
			name:    "65-char hex (one over)",
			input:   "e88a691e98da9e73cfc6a93bd2e6c9a3ff5df66ea71abb95f5e2b1b5e5e7e3f1aa",
			wantErr: true,
		},
		{
			name:    "all zeros (valid format)",
			input:   "0000000000000000000000000000000000000000000000000000000000000000",
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePubkey(tc.input)
			if tc.wantErr && err == nil {
				t.Errorf("ValidatePubkey(%q) = nil, want non-nil error", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidatePubkey(%q) = %v, want nil", tc.input, err)
			}
		})
	}
}

// TestIsValidHexPubkey mirrors TestValidatePubkey for the unexported bool
// fast-path used in hot loops.
func TestIsValidHexPubkey(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "valid lowercase 64-char hex",
			input: "e88a691e98da9e73cfc6a93bd2e6c9a3ff5df66ea71abb95f5e2b1b5e5e7e3f1",
			want:  true,
		},
		{
			name:  "uppercase 64-char hex",
			input: "83E818DFED1B3A9DBBD3EBE4AE1FC0EB9614DB3B4C6B1765BF1E3B9DFE0C5F2A",
			want:  false,
		},
		{
			name:  "short hex: f1",
			input: "f1",
			want:  false,
		},
		{
			name:  "short hex: cbdc",
			input: "cbdc",
			want:  false,
		},
		{
			name:  "short hex: de",
			input: "de",
			want:  false,
		},
		{
			name:  "relay-URL blob",
			input: "0115wss://relay.mostr.pub/did:plc:user-0000000000000000000000000000000000000000000000000000000000000000",
			want:  false,
		},
		{
			name:  "empty string",
			input: "",
			want:  false,
		},
		{
			name:  "all zeros (valid format)",
			input: "0000000000000000000000000000000000000000000000000000000000000000",
			want:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isValidHexPubkey(tc.input)
			if got != tc.want {
				t.Errorf("isValidHexPubkey(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
