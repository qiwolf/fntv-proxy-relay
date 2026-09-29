package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// STRMDirectoryMapping maps a media server's path to a local synchronized copy.
// It does not mount or synchronize files and never rewrites the URL inside STRM.
type STRMDirectoryMapping struct {
	Source string `mapstructure:"source" yaml:"source"`
	Local  string `mapstructure:"local" yaml:"local"`
}

func validSTRMDirectory(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && p != "/" && !strings.ContainsAny(p, "\\\x00\r\n")
}

func ValidateSTRMDirectoryMap(mappings []STRMDirectoryMapping) error {
	seen := map[string]bool{}
	for _, m := range mappings {
		if !validSTRMDirectory(m.Source) || !validSTRMDirectory(m.Local) {
			return fmt.Errorf("strm_directory_map requires normalized absolute source/local directories")
		}
		if seen[m.Source] {
			return fmt.Errorf("duplicate strm_directory_map source")
		}
		seen[m.Source] = true
	}
	return nil
}

// MapSTRMDirectory uses the longest directory-boundary prefix. When mappings
// exist, unmatched or non-normalized input fails closed.
func MapSTRMDirectory(p string, mappings []STRMDirectoryMapping) (string, error) {
	if len(mappings) == 0 {
		return p, nil
	}
	if err := ValidateSTRMDirectoryMap(mappings); err != nil {
		return "", err
	}
	if !validSTRMDirectory(p) {
		return "", fmt.Errorf("invalid STRM source path")
	}
	best := -1
	for i, m := range mappings {
		if p == m.Source || strings.HasPrefix(p, m.Source+string(filepath.Separator)) {
			if best < 0 || len(m.Source) > len(mappings[best].Source) {
				best = i
			}
		}
	}
	if best < 0 {
		return "", fmt.Errorf("STRM source path has no directory mapping")
	}
	m := mappings[best]
	relative, err := filepath.Rel(m.Source, p)
	if err != nil {
		return "", err
	}
	return filepath.Join(m.Local, relative), nil
}

func (c *Config) GetSTRMDirectoryMap() []STRMDirectoryMapping {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return append([]STRMDirectoryMapping(nil), c.STRMDirectoryMap...)
}
