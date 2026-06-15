package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SessionLink records the association between a local repo and the remote
// working tree a bundle upload produced. It carries everything needed to reach
// the linked repo again — the bridge address, TLS verification details, the link
// id, and the extracted remote path — except the bearer token, which is always
// supplied separately (never persisted to the link file).
type SessionLink struct {
	// Address is host:port of the remote bridge.
	Address string `json:"address"`
	// ServerName overrides TLS/SNI verification; empty => host of Address.
	ServerName string `json:"server_name,omitempty"`
	// CACertFile is the CA trusted for the bridge cert (for a self-signed bridge).
	CACertFile string `json:"ca_cert_file,omitempty"`
	// LinkID is the per-link identifier (a single path component on the remote).
	LinkID string `json:"link_id"`
	// RemotePath is the extracted working tree on the remote; use it as --cwd for
	// a remote run/attach against the linked repo.
	RemotePath string `json:"remote_path"`
	// BundleSHA256 is the hex SHA-256 of the uploaded bundle, for verification.
	BundleSHA256 string `json:"bundle_sha256,omitempty"`
}

// Validate checks the fields required to use a link.
func (l SessionLink) Validate() error {
	if strings.TrimSpace(l.Address) == "" {
		return errors.New("remote: session link address is required")
	}
	if strings.TrimSpace(l.LinkID) == "" {
		return errors.New("remote: session link id is required")
	}
	if strings.TrimSpace(l.RemotePath) == "" {
		return errors.New("remote: session link remote path is required")
	}
	return nil
}

// Save writes the link to path as pretty JSON with 0600 permissions, atomically
// (write-temp-then-rename) so a reader never sees a partial file.
func (l SessionLink) Save(path string) error {
	if err := l.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, append(data, '\n'), 0o600)
}

// LoadSessionLink reads and validates a link file written by Save.
func LoadSessionLink(path string) (*SessionLink, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var l SessionLink
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("remote: decode session link: %w", err)
	}
	if err := l.Validate(); err != nil {
		return nil, err
	}
	return &l, nil
}

// atomicWriteFile writes data to path via a temp file in the same directory,
// chmod-ed to perm, then renamed into place.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".link-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
