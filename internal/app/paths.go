package app

import (
	"fmt"
	"os"
	"path/filepath"
)

// AppName is used for the support directory and the window/menu-bar title.
const AppName = "DerbyAndAles"

// Paths locates everything the app writes. All of it lives under one directory
// so that backing up the club's data is a single folder copy.
type Paths struct {
	Root    string // ~/Library/Application Support/DerbyAndAles
	DB      string // derby.sqlite3
	Photos  string // photos/  (content-addressed originals)
	Renders string // photos/renders/  (derived sizes, safe to delete)
	Backups string // backups/
	Certs   string // certs/   (self-signed TLS for LAN camera access)
	Traces  string // traces/  (recorded timer serial sessions)
}

// DefaultPaths resolves the standard macOS support directory. When root is
// non-empty it is used instead, which is how the tests and the -data flag get
// an isolated installation.
func DefaultPaths(root string) (Paths, error) {
	if root == "" {
		base, err := os.UserConfigDir() // ~/Library/Application Support on darwin
		if err != nil {
			return Paths{}, fmt.Errorf("locate support directory: %w", err)
		}
		root = filepath.Join(base, AppName)
	}
	p := Paths{
		Root:    root,
		DB:      filepath.Join(root, "derby.sqlite3"),
		Photos:  filepath.Join(root, "photos"),
		Renders: filepath.Join(root, "photos", "renders"),
		Backups: filepath.Join(root, "backups"),
		Certs:   filepath.Join(root, "certs"),
		Traces:  filepath.Join(root, "traces"),
	}
	return p, nil
}

// EnsureDirs creates every directory the app needs.
func (p Paths) EnsureDirs() error {
	for _, dir := range []string{p.Root, p.Photos, p.Renders, p.Backups, p.Certs, p.Traces} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}
