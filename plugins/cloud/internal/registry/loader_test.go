package registry

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	proto "github.com/convergent-systems-co/aish/libs/proto/registry"
)

// installFakePlugin writes a fake binary into binDir, signs it with
// the dev key, and installs the manifest under root.
func installFakePlugin(t *testing.T, root, name string) (proto.Manifest, string) {
	t.Helper()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, name+"-bin")
	if err := os.WriteFile(binPath, []byte("fake-"+name+"\n"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	m, err := Install(root, InstallSource{
		Name:       name,
		Version:    "0.0.1",
		BinaryPath: binPath,
		Kinds:      []proto.Kind{proto.KindInference},
		SignerID:   "aish-dev",
		PrivateKey: proto.DevPrivateKey(),
	}, InstallOpts{})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	return m, binPath
}

func TestInstall_OneValidPluginThenProtoLoad(t *testing.T) {
	root := t.TempDir()
	installFakePlugin(t, root, "fake")
	entries, err := proto.Load(root, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Manifest.Name != "fake" {
		t.Fatalf("entry name = %q want %q", entries[0].Manifest.Name, "fake")
	}
}

func TestInstall_RefusesDuplicateWithoutForce(t *testing.T) {
	root := t.TempDir()
	installFakePlugin(t, root, "twice")
	// Second install of the same name without --force should fail.
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "twice-bin")
	_ = os.WriteFile(binPath, []byte("again\n"), 0o755)
	_, err := Install(root, InstallSource{
		Name:       "twice",
		Version:    "0.0.2",
		BinaryPath: binPath,
		Kinds:      []proto.Kind{proto.KindInference},
		SignerID:   "aish-dev",
		PrivateKey: proto.DevPrivateKey(),
	}, InstallOpts{})
	if !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("expected ErrAlreadyInstalled, got %v", err)
	}
}

func TestInstall_OverwritesWithForce(t *testing.T) {
	root := t.TempDir()
	installFakePlugin(t, root, "twice")
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "twice-bin")
	_ = os.WriteFile(binPath, []byte("again\n"), 0o755)
	m2, err := Install(root, InstallSource{
		Name:       "twice",
		Version:    "0.0.2",
		BinaryPath: binPath,
		Kinds:      []proto.Kind{proto.KindInference},
		SignerID:   "aish-dev",
		PrivateKey: proto.DevPrivateKey(),
	}, InstallOpts{Force: true})
	if err != nil {
		t.Fatalf("Install --force failed: %v", err)
	}
	if m2.Version != "0.0.2" {
		t.Fatalf("force install didn't update version: %s", m2.Version)
	}
}

func TestInstall_RejectsUnknownSigner(t *testing.T) {
	root := t.TempDir()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "x-bin")
	_ = os.WriteFile(binPath, []byte("x\n"), 0o755)
	_, err := Install(root, InstallSource{
		Name:       "x",
		Version:    "0.0.1",
		BinaryPath: binPath,
		Kinds:      []proto.Kind{proto.KindInference},
		SignerID:   "not-in-anchors", // not in trust list
		PrivateKey: proto.DevPrivateKey(),
	}, InstallOpts{})
	if err == nil {
		t.Fatalf("expected install to refuse unknown signer")
	}
	if !errors.Is(err, proto.ErrUnknownSigner) {
		t.Fatalf("expected ErrUnknownSigner, got %v", err)
	}
}

func TestRemove_DeletesManifest(t *testing.T) {
	root := t.TempDir()
	installFakePlugin(t, root, "rm")
	if err := Remove(root, "rm", nil); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "rm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected plugin dir removed, stat err=%v", err)
	}
}

func TestRemove_NotFound(t *testing.T) {
	root := t.TempDir()
	err := Remove(root, "ghost", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVerifyInstalled_HappyPath(t *testing.T) {
	root := t.TempDir()
	installFakePlugin(t, root, "v")
	if _, err := VerifyInstalled(root, "v"); err != nil {
		t.Fatalf("VerifyInstalled: %v", err)
	}
}

func TestVerifyInstalled_TamperedBinary(t *testing.T) {
	root := t.TempDir()
	_, binPath := installFakePlugin(t, root, "v")
	// Tamper the binary after install.
	if err := os.WriteFile(binPath, []byte("tampered\n"), 0o755); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	_, err := VerifyInstalled(root, "v")
	if !errors.Is(err, proto.ErrBinaryHashMismatch) {
		t.Fatalf("expected ErrBinaryHashMismatch, got %v", err)
	}
}

func TestInstall_ConcurrentSerialisedByLock(t *testing.T) {
	root := t.TempDir()
	// Two goroutines try to install different plugins concurrently.
	// The lock should serialise them; both should succeed because
	// names are distinct.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, name := range []string{"alpha", "beta"} {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			binDir := t.TempDir()
			binPath := filepath.Join(binDir, name+"-bin")
			_ = os.WriteFile(binPath, []byte(name+"\n"), 0o755)
			_, err := Install(root, InstallSource{
				Name:       name,
				Version:    "0.0.1",
				BinaryPath: binPath,
				Kinds:      []proto.Kind{proto.KindInference},
				SignerID:   "aish-dev",
				PrivateKey: proto.DevPrivateKey(),
			}, InstallOpts{})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatalf("concurrent install failed: %v", e)
		}
	}
	entries, err := proto.Load(root, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries after concurrent install, got %d", len(entries))
	}
}
