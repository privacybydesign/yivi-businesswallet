//go:build integration

package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
)

// Fingerprint is a hash of every embedded migration's name and contents, so
// a database migrated once can be reused until a migration changes.
func Fingerprint() (string, error) {
	h := sha256.New()
	err := fs.WalkDir(migrationsFS, migrationsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := migrationsFS.ReadFile(path)
		if err != nil {
			return err
		}
		h.Write([]byte(path))
		h.Write([]byte{0})
		h.Write(b)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
