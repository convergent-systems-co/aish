package telemetry

import (
	"os"
)

// readDirOrEmpty returns os.ReadDir(path) but converts ENOENT into an
// empty slice + nil error — common case in tests where the dir is
// conditionally created.
func readDirOrEmpty(path string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return entries, err
}

// writeFile is a thin wrapper for tests seeding fixture files.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
