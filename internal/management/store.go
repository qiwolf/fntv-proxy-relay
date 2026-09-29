package management

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var ErrVersionConflict = errors.New("configuration version conflict")
var ErrDocumentNotFound = errors.New("configuration not found")

// Document is an opaque configuration snapshot. Version zero means no document.
type Document struct {
	Version uint64          `json:"version"`
	Data    json.RawMessage `json:"data"`
}

// DocumentStore owns a private directory; callers must not expose its contents.
type DocumentStore struct {
	dir      string
	maxBytes int64
}

func NewDocumentStore(dir string, maxBytes int64) (*DocumentStore, error) {
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	// Reject symlinks in every existing component, including the final directory.
	if err = privateDirectory(abs); err != nil {
		return nil, err
	}
	return &DocumentStore{abs, maxBytes}, nil
}

func privateDirectory(path string) error {
	parent := filepath.Dir(path)
	if parent != path {
		info, err := os.Lstat(parent)
		if os.IsNotExist(err) {
			if err = privateDirectory(parent); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("unsafe parent directory")
			}
			for p := filepath.Dir(parent); ; p = filepath.Dir(p) {
				i, e := os.Lstat(p)
				if e != nil {
					return e
				}
				if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
					return errors.New("unsafe directory ancestry")
				}
				if filepath.Dir(p) == p {
					break
				}
			}
		}
	}
	if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe storage directory")
	}
	return os.Chmod(path, 0700)
}

func (s *DocumentStore) locked(fn func() error) error {
	info, err := os.Lstat(s.dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe storage directory")
	}
	f, err := openStoreLock(filepath.Join(s.dir, ".lock"))
	if err != nil {
		return err
	}
	defer f.Close()
	if err = lockStore(f); err != nil {
		return err
	}
	defer unlockStore(f)
	return fn()
}

func (s *DocumentStore) read(name string) (Document, error) {
	var d Document
	path := filepath.Join(s.dir, name)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return d, ErrDocumentNotFound
	}
	if err != nil {
		return d, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return d, errors.New("unsafe document permissions or type")
	}
	f, err := openStoreRead(path)
	if err != nil {
		return d, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return d, err
	}
	if !opened.Mode().IsRegular() || opened.Mode().Perm()&0077 != 0 || !os.SameFile(info, opened) {
		return d, errors.New("document changed during open")
	}
	b, err := io.ReadAll(io.LimitReader(f, s.maxBytes+1025))
	if err != nil {
		return d, err
	}
	if int64(len(b)) > s.maxBytes+1024 {
		return d, errors.New("document exceeds size limit")
	}
	if err = json.Unmarshal(b, &d); err != nil {
		return d, fmt.Errorf("corrupt document: %w", err)
	}
	if d.Version == 0 || !json.Valid(d.Data) || int64(len(d.Data)) > s.maxBytes {
		return d, errors.New("invalid document")
	}
	return d, nil
}

func (s *DocumentStore) Read() (d Document, err error) {
	err = s.locked(func() error { var e error; d, e = s.read("current.json"); return e })
	return
}

// WithVersion holds the cross-process configuration lock for a dependent action.
// The callback must not call this store again (including through another instance
// using the same directory). It may use a store in a separate directory.
func (s *DocumentStore) WithVersion(expected uint64, action func(Document) error) error {
	if action == nil {
		return errors.New("action required")
	}
	return s.locked(func() error {
		d, err := s.read("current.json")
		if err != nil {
			return err
		}
		if d.Version != expected {
			return ErrVersionConflict
		}
		return action(d)
	})
}

func (s *DocumentStore) write(name string, d Document) error {
	path := filepath.Join(s.dir, name)
	if i, e := os.Lstat(path); e == nil {
		if !i.Mode().IsRegular() {
			return errors.New("unsafe document destination")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".pending-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	directory, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (s *DocumentStore) save(expected uint64, data json.RawMessage) (Document, error) {
	var empty Document
	if !json.Valid(data) || int64(len(data)) > s.maxBytes {
		return empty, errors.New("invalid JSON or size limit exceeded")
	}
	current, err := s.read("current.json")
	if err != nil && !errors.Is(err, ErrDocumentNotFound) {
		return empty, err
	}
	if current.Version != expected {
		return empty, ErrVersionConflict
	}
	if expected == ^uint64(0) {
		return empty, errors.New("version exhausted")
	}
	if current.Version > 0 {
		name := fmt.Sprintf("backup-%d.json", current.Version)
		old, e := s.read(name)
		if e == nil {
			if old.Version != current.Version || string(old.Data) != string(current.Data) {
				return empty, errors.New("backup conflict")
			}
		} else if errors.Is(e, ErrDocumentNotFound) {
			if e = s.write(name, current); e != nil {
				return empty, e
			}
		} else {
			return empty, e
		}
	}
	next := Document{expected + 1, append(json.RawMessage(nil), data...)}
	if err = s.write("current.json", next); err != nil {
		return empty, err
	}
	return next, nil
}

func (s *DocumentStore) Save(expectedVersion uint64, data json.RawMessage) (d Document, err error) {
	err = s.locked(func() error { var e error; d, e = s.save(expectedVersion, data); return e })
	return
}

// Restore never changes a backup; it saves that content as a new current version.
func (s *DocumentStore) Restore(expectedVersion, backupVersion uint64) (d Document, err error) {
	err = s.locked(func() error {
		b, e := s.read(fmt.Sprintf("backup-%d.json", backupVersion))
		if e != nil {
			return e
		}
		if b.Version != backupVersion {
			return errors.New("backup version mismatch")
		}
		d, e = s.save(expectedVersion, b.Data)
		return e
	})
	return
}

func (s *DocumentStore) Backups() (versions []uint64, err error) {
	err = s.locked(func() error {
		entries, e := os.ReadDir(s.dir)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			n := entry.Name()
			if !strings.HasPrefix(n, "backup-") || !strings.HasSuffix(n, ".json") {
				continue
			}
			v, e := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(n, "backup-"), ".json"), 10, 64)
			if e != nil {
				return errors.New("invalid backup name")
			}
			d, e := s.read(n)
			if e != nil {
				return e
			}
			if d.Version != v {
				return errors.New("backup version mismatch")
			}
			versions = append(versions, v)
		}
		sort.Slice(versions, func(i, j int) bool { return versions[i] > versions[j] })
		return nil
	})
	return
}
