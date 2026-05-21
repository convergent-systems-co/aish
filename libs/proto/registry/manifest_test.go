package registry

import (
	"errors"
	"strings"
	"testing"
)

// validBaseManifest is the manifest the table-driven tests start from
// and mutate one field at a time. Signature is a syntactically-valid
// 64-byte base64 ed25519 signature (not crypto-valid; Validate does
// not verify the signature math — that's verify.go's job).
func validBaseManifest() Manifest {
	return Manifest{
		FormatVersion: CurrentFormatVersion,
		Name:          "cloud",
		Version:       "0.3.2",
		BinaryPath:    "/usr/local/bin/aish-inference-cloud",
		Kinds:         []Kind{KindInference},
		SHA256:        strings.Repeat("ab", 32), // 64 hex chars
		SignerID:      "aish-dev",
		Signature:     strings.Repeat("A", 87) + "=", // 88 base64 chars
		CreatedAt:     "2026-05-21T00:00:00Z",
	}
}

func TestManifestValidate_HappyPath(t *testing.T) {
	m := validBaseManifest()
	if err := m.Validate(); err != nil {
		t.Fatalf("happy-path manifest should validate, got %v", err)
	}
}

func TestManifestValidate_RejectsMalformed(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Manifest)
		want   string
	}{
		{
			name:   "format_version zero",
			mutate: func(m *Manifest) { m.FormatVersion = 0 },
			want:   "format_version",
		},
		{
			name:   "name empty",
			mutate: func(m *Manifest) { m.Name = "" },
			want:   "name",
		},
		{
			name:   "name with uppercase",
			mutate: func(m *Manifest) { m.Name = "Cloud" },
			want:   "name",
		},
		{
			name:   "name with leading hyphen",
			mutate: func(m *Manifest) { m.Name = "-cloud" },
			want:   "name",
		},
		{
			name:   "name with slash",
			mutate: func(m *Manifest) { m.Name = "cloud/x" },
			want:   "name",
		},
		{
			name:   "version empty",
			mutate: func(m *Manifest) { m.Version = "  " },
			want:   "version",
		},
		{
			name:   "binary_path empty",
			mutate: func(m *Manifest) { m.BinaryPath = "" },
			want:   "binary_path",
		},
		{
			name:   "binary_path relative",
			mutate: func(m *Manifest) { m.BinaryPath = "bin/aish-cloud" },
			want:   "binary_path",
		},
		{
			name:   "kinds empty",
			mutate: func(m *Manifest) { m.Kinds = nil },
			want:   "kinds",
		},
		{
			name:   "kinds contains empty entry",
			mutate: func(m *Manifest) { m.Kinds = []Kind{""} },
			want:   "kinds",
		},
		{
			name:   "sha256 empty",
			mutate: func(m *Manifest) { m.SHA256 = "" },
			want:   "sha256",
		},
		{
			name:   "sha256 wrong length",
			mutate: func(m *Manifest) { m.SHA256 = "abcd" },
			want:   "sha256",
		},
		{
			name:   "sha256 not hex",
			mutate: func(m *Manifest) { m.SHA256 = strings.Repeat("z", 64) },
			want:   "sha256",
		},
		{
			name:   "signer_id empty",
			mutate: func(m *Manifest) { m.SignerID = "" },
			want:   "signer_id",
		},
		{
			name:   "signature empty",
			mutate: func(m *Manifest) { m.Signature = "" },
			want:   "signature",
		},
		{
			name:   "signature not base64",
			mutate: func(m *Manifest) { m.Signature = "!!! not base64 !!!" },
			want:   "signature",
		},
		{
			name:   "created_at empty",
			mutate: func(m *Manifest) { m.CreatedAt = "" },
			want:   "created_at",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validBaseManifest()
			tc.mutate(&m)
			err := m.Validate()
			if err == nil {
				t.Fatalf("expected ErrManifestMalformed, got nil")
			}
			if !errors.Is(err, ErrManifestMalformed) {
				t.Fatalf("expected wrapping ErrManifestMalformed; got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v should mention field %q", err, tc.want)
			}
		})
	}
}

func TestManifestValidate_AcceptsWindowsAbsolutePath(t *testing.T) {
	m := validBaseManifest()
	m.BinaryPath = `C:\Program Files\aish\aish-inference-cloud.exe`
	if err := m.Validate(); err != nil {
		t.Fatalf("windows absolute path should validate: %v", err)
	}
}

func TestManifestValidate_AcceptsUNCPath(t *testing.T) {
	m := validBaseManifest()
	m.BinaryPath = `\\server\share\aish-inference-cloud.exe`
	if err := m.Validate(); err != nil {
		t.Fatalf("UNC path should validate: %v", err)
	}
}

func TestManifestHasKind(t *testing.T) {
	m := validBaseManifest()
	if !m.HasKind(KindInference) {
		t.Fatalf("HasKind(inference) should be true")
	}
	if m.HasKind(Kind("unknown")) {
		t.Fatalf("HasKind(unknown) should be false")
	}
}

func TestAllKinds_NonEmpty(t *testing.T) {
	if len(AllKinds()) == 0 {
		t.Fatalf("AllKinds() must enumerate at least KindInference")
	}
	found := false
	for _, k := range AllKinds() {
		if k == KindInference {
			found = true
		}
	}
	if !found {
		t.Fatalf("AllKinds() must include KindInference")
	}
}
