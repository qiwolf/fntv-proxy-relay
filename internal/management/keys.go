// Package management contains isolated management storage, separate from runtime secrets.
package management

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// KeyStore owns a private directory. It never follows configurable key paths.
type KeyStore struct {
	dir string
	mu  sync.Mutex
}

var ErrCorrupt = errors.New("key store is invalid; existing data was preserved")

func NewKeyStore(dir string) (*KeyStore, error) {
	if !filepath.IsAbs(dir) {
		return nil, errors.New("management directory must be absolute")
	}
	if info, err := os.Lstat(dir); err == nil && (info.Mode().Perm()&0077 != 0 || !info.IsDir()) {
		return nil, errors.New("management directory must be private (0700), not a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := privateDirectory(dir); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("management directory must be private (0700), not a symlink")
	}
	return &KeyStore{dir: dir}, nil
}
func validService(s string) bool { return s == "fntv" || s == "emby" || s == "jellyfin" }
func (s *KeyStore) read() (map[string]string, error) {
	path := filepath.Join(s.dir, "keys.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, ErrCorrupt
	}
	f, err := openStoreRead(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Mode().Perm()&0077 != 0 {
		return nil, ErrCorrupt
	}
	raw, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return nil, err
	}
	if len(raw) > 4096 {
		return nil, ErrCorrupt
	}
	var keys map[string]string
	if json.Unmarshal(raw, &keys) != nil || keys == nil {
		return nil, ErrCorrupt
	}
	for id, key := range keys {
		decoded, e := hex.DecodeString(key)
		if !validService(id) || e != nil || len(decoded) != 32 {
			return nil, ErrCorrupt
		}
	}
	return keys, nil
}

// Status returns availability only, never key material.
func (s *KeyStore) Status() (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result map[string]bool
	err := (&DocumentStore{dir: s.dir}).locked(func() error { var e error; result, e = s.status(); return e })
	return result, err
}
func (s *KeyStore) status() (map[string]bool, error) {
	keys, e := s.read()
	if e != nil {
		return nil, e
	}
	out := map[string]bool{}
	for _, id := range []string{"fntv", "emby", "jellyfin"} {
		_, out[id] = keys[id]
	}
	return out, nil
}

// Generate creates missing keys in one atomic snapshot; existing keys are unchanged.
func (s *KeyStore) Generate(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return (&DocumentStore{dir: s.dir}).locked(func() error { return s.generate(ids) })
}
func (s *KeyStore) generate(ids []string) error {
	if len(ids) == 0 {
		return errors.New("select at least one service")
	}
	for _, id := range ids {
		if !validService(id) {
			return errors.New("unknown service")
		}
	}
	keys, e := s.read()
	if e != nil {
		return e
	}
	changed := false
	for _, id := range ids {
		if _, exists := keys[id]; exists {
			continue
		}
		var key [32]byte
		if _, e = rand.Read(key[:]); e != nil {
			return e
		}
		keys[id] = hex.EncodeToString(key[:])
		changed = true
	}
	if !changed {
		return nil
	}
	return s.writeKeys(keys)
}

func (s *KeyStore) writeKeys(keys map[string]string) error {
	raw, e := json.Marshal(keys)
	if e != nil {
		return e
	}
	file, e := os.CreateTemp(s.dir, ".keys-*")
	if e != nil {
		return e
	}
	defer os.Remove(file.Name())
	if _, e = file.Write(raw); e != nil {
		file.Close()
		return e
	}
	if e = file.Sync(); e != nil {
		file.Close()
		return e
	}
	if e = file.Close(); e != nil {
		return e
	}
	if e = os.Rename(file.Name(), filepath.Join(s.dir, "keys.json")); e != nil {
		return e
	}
	directory, e := os.Open(s.dir)
	if e != nil {
		return e
	}
	defer directory.Close()
	return directory.Sync()
}

// Material is for internal runtime/configuration packaging only. Never serialize
// its result in an HTTP status response or ordinary configuration export.
func (s *KeyStore) Material(ids []string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := map[string]string{}
	err := (&DocumentStore{dir: s.dir}).locked(func() error {
		keys, err := s.read()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if !validService(id) || keys[id] == "" {
				return errors.New("selected service has no shared key")
			}
			result[id] = keys[id]
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// importKeys validates the entire set before the single atomic write.
func (s *KeyStore) importKeys(incoming map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return (&DocumentStore{dir: s.dir}).locked(func() error {
		keys, err := s.read()
		if err != nil {
			return err
		}
		for id, key := range incoming {
			decoded, e := hex.DecodeString(key)
			if !validService(id) || e != nil || len(decoded) != 32 {
				return errors.New("invalid shared key package")
			}
			if old, ok := keys[id]; ok && old != key {
				return errors.New("shared key conflicts with existing key; no keys were changed")
			}
		}
		for id, key := range incoming {
			keys[id] = key
		}
		return s.writeKeys(keys)
	})
}
