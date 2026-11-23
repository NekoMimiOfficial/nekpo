package pkgdb

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"nekpo/internal/paths"
)

type Source struct {
	Kind   string `json:"kind"`
	URL    string `json:"url,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Server string `json:"server,omitempty"`
}

type Package struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Commit    string   `json:"commit,omitempty"`
	Source    Source   `json:"source"`
	Artifact  string   `json:"artifact"`
	Kind      string   `json:"kind"`
	Dir       string   `json:"dir"`
	Links     []string `json:"links,omitempty"`
	Files     []string `json:"files,omitempty"`
	Installed int64    `json:"installed"`
}

type DB struct {
	Packages map[string]*Package `json:"packages"`
}

func Load() (*DB, error) {
	db := &DB{Packages: map[string]*Package{}}
	raw, err := os.ReadFile(paths.DBFile())
	if errors.Is(err, os.ErrNotExist) {
		return db, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, db); err != nil {
		return nil, err
	}
	if db.Packages == nil {
		db.Packages = map[string]*Package{}
	}
	return db, nil
}

func (db *DB) Save() error {
	if err := paths.Ensure(paths.DataDir()); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		return err
	}
	tmp := paths.DBFile() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, paths.DBFile())
}

func (db *DB) Get(id string) *Package { return db.Packages[id] }

func (db *DB) Put(p *Package) {
	if p.Installed == 0 {
		p.Installed = time.Now().Unix()
	}
	db.Packages[p.ID] = p
}

func (db *DB) Delete(id string) { delete(db.Packages, id) }

func (db *DB) All() []*Package {
	out := make([]*Package, 0, len(db.Packages))
	for _, p := range db.Packages {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func DirName(name, id, version string) string {
	return filepath.Join(paths.VarDir(), clean(name)+"_"+clean(id)+"_"+clean(version))
}

func clean(s string) string {
	b := []byte(s)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '+') {
			b[i] = '_'
		}
	}
	return string(b)
}
