package depsclient

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"nekpo/internal/archive"
	"nekpo/internal/depsconf"
	"nekpo/internal/proto"
	"nekpo/internal/ui"
)

const MarkerName = ".nekpo-dep"

type Marker struct {
	Lang    string `json:"lang"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Arch    string `json:"arch"`
	SHA256  string `json:"sha256"`
	Server  string `json:"server"`
	Fetched int64  `json:"fetched"`
}

type SyncOptions struct {
	Root     string
	Server   string
	Force    bool
	Update   bool
	NoLock   bool
	LockOnly bool
	Insecure bool
}

func DestDir(root string, d depsconf.Dep, used map[string]string) string {
	base := filepath.Join(root, "deps", d.Lang)
	if d.Lang == "cpp" {
		base = filepath.Join(root, "third_party")
	}
	key := filepath.Join(base, d.Name)
	if owner, ok := used[key]; ok && owner != d.Key() {
		key = filepath.Join(base, d.Name+"-"+d.Arch)
	}
	used[key] = d.Key()
	return key
}

func readMarker(dir string) *Marker {
	raw, err := os.ReadFile(filepath.Join(dir, MarkerName))
	if err != nil {
		return nil
	}
	var m Marker
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return &m
}

func Fetch(ctx context.Context, c *Client, info *proto.DepInfo, dest, tmpRoot string, force bool) (bool, error) {
	if st, err := os.Stat(dest); err == nil && st.IsDir() {
		m := readMarker(dest)
		if m != nil && m.Version == info.Version && m.SHA256 == info.SHA256 {
			return false, nil
		}
		if !force {
			if m != nil {
				ui.Warn("%s: %s has %s, wanted %s; left as is (use -force to replace)", info.Name, dest, m.Version, info.Version)
			} else {
				ui.Note("%s: %s already exists and is not managed by nekpo; left as is", info.Name, dest)
			}
			return false, nil
		}
		if err := os.RemoveAll(dest); err != nil {
			return false, err
		}
	}
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return false, err
	}
	p := ui.Start(fmt.Sprintf("dependency %s %s (%s)", info.Name, info.Version, info.Arch))
	err := func() error {
		file := filepath.Join(tmpRoot, info.File)
		if err := c.Download(ctx, info, file, p); err != nil {
			return err
		}
		defer os.Remove(file)
		p.Note("extracting")
		stage := filepath.Join(tmpRoot, info.Name+".x")
		os.RemoveAll(stage)
		if err := archive.Extract(file, stage, true); err != nil {
			os.RemoveAll(stage)
			return err
		}
		m := Marker{Lang: info.Lang, Name: info.Name, Version: info.Version, Arch: info.Arch, SHA256: info.SHA256, Server: c.Server, Fetched: time.Now().Unix()}
		raw, _ := json.Marshal(m)
		if err := os.WriteFile(filepath.Join(stage, MarkerName), raw, 0o644); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.Rename(stage, dest)
	}()
	p.Finish(err)
	return err == nil, err
}

func Sync(ctx context.Context, o SyncOptions) error {
	confPath := filepath.Join(o.Root, "nekpo", depsconf.ConfName)
	f, err := depsconf.Parse(confPath)
	if err != nil {
		return err
	}
	if len(f.Deps) == 0 {
		return nil
	}
	server := f.Server
	if server == "" {
		server = o.Server
	}
	if server == "" {
		return fmt.Errorf("no server for dependencies: set server in %s, in the nekpo config, or NEKPO_SERVER", depsconf.ConfName)
	}
	if err := CheckServer(server, o.Insecure); err != nil {
		return err
	}
	lockPath := filepath.Join(o.Root, "nekpo", depsconf.LockName)
	lock := &depsconf.Lock{}
	var old *depsconf.Lock
	if _, err := os.Stat(lockPath); err == nil {
		if old, err = depsconf.ParseLock(lockPath); err != nil {
			return err
		}
		if !o.Update {
			lock = old
		}
	}
	c := New(server)
	tmpRoot := filepath.Join(o.Root, ".nekpo-deps-tmp")
	defer os.RemoveAll(tmpRoot)
	used := map[string]string{}
	changed := false
	for _, d := range f.Deps {
		want := d.Version
		var locked *depsconf.LockEntry
		if !o.Update {
			locked = lock.Find(d)
			if want == "" && locked != nil {
				want = locked.Version
			}
		}
		info, err := c.Info(ctx, d.Lang, d.Arch, d.Name, want)
		if err != nil {
			return fmt.Errorf("%s: %w", d, err)
		}
		if locked != nil && locked.Version == info.Version && locked.SHA256 != "" && locked.SHA256 != info.SHA256 {
			return fmt.Errorf("%s %s: the server's file no longer matches %s; run nekpo deps sync -update if that is expected", d.Name, info.Version, depsconf.LockName)
		}
		dest := DestDir(o.Root, d, used)
		if !o.LockOnly {
			if _, err := Fetch(ctx, c, info, dest, tmpRoot, o.Force); err != nil {
				return fmt.Errorf("%s: %w", d, err)
			}
		}
		entry := depsconf.LockEntry{Dep: depsconf.Dep{Lang: d.Lang, Name: d.Name, Version: info.Version, Arch: d.Arch}, SHA256: info.SHA256}
		if prev := lock.Find(d); prev == nil || prev.Version != entry.Version || prev.SHA256 != entry.SHA256 {
			changed = true
		}
		lock.Set(entry)
	}
	if o.NoLock {
		return nil
	}
	if changed || old == nil || o.Update {
		if err := lock.Save(lockPath); err != nil {
			return err
		}
		ui.Note("wrote %s", lockPath)
	}
	return nil
}
