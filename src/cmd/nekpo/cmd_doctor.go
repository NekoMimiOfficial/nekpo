package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"nekpo/internal/builder"
	"nekpo/internal/depsconf"
	"nekpo/internal/paths"
	"nekpo/internal/pkgdb"
	"nekpo/internal/proto"
	"nekpo/internal/ui"
)

type report struct{ fails, warns int }

func (r *report) ok(format string, a ...any) { ui.OK(format, a...) }
func (r *report) warn(format string, a ...any) {
	r.warns++
	ui.Warn(format, a...)
}
func (r *report) fail(format string, a ...any) {
	r.fails++
	ui.Fail(format, a...)
}

func toolVersion(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	if line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n"); line != "" {
		return line
	}
	return ""
}

func writable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".nekpo-doctor-")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func cmdDoctor(ctx context.Context, args []string) error {
	fs := newFlags("doctor")
	cfg := fs.String("config", "", "config file")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	r := &report{}
	ui.Info("nekpo %s on %s/%s", Version, runtime.GOOS, runtime.GOARCH)

	ui.Info("tools")
	for _, t := range []struct {
		name     string
		required bool
		note     string
		args     []string
	}{
		{"git", true, "needed to clone and tag projects", []string{"--version"}},
		{"bash", true, "runs nekpo/build.sh", []string{"--version"}},
		{"cmake", false, "needed by generated C and C++ projects", []string{"--version"}},
		{"ninja", false, "optional faster CMake generator", []string{"--version"}},
		{"make", false, "default CMake generator when ninja is missing", []string{"--version"}},
		{"gzip", false, "used while building Debian changelogs", nil},
		{"dpkg-deb", false, "needed to build .deb packages", []string{"--version"}},
		{"curl", false, "used by the AppImage script to fetch linuxdeploy", []string{"--version"}},
	} {
		if _, err := exec.LookPath(t.name); err != nil {
			if t.required {
				r.fail("%s not found: %s", t.name, t.note)
			} else {
				r.warn("%s not found: %s", t.name, t.note)
			}
			continue
		}
		v := ""
		if t.args != nil {
			v = toolVersion(t.name, t.args...)
		}
		if v != "" {
			r.ok("%s: %s", t.name, v)
		} else {
			r.ok("%s", t.name)
		}
	}
	if os.Getenv("VITASDK") != "" {
		r.ok("VITASDK=%s", os.Getenv("VITASDK"))
	} else {
		ui.Note("VITASDK is not set (only needed for PS Vita builds)")
	}

	ui.Info("folders")
	for _, d := range []string{paths.DataDir(), paths.VarDir(), paths.LogDir(), paths.BinDir()} {
		if err := writable(d); err != nil {
			r.fail("%s is not writable: %v", d, err)
		} else {
			r.ok("%s", d)
		}
	}
	if !binOnPath() {
		r.warn("%s is not in PATH; installed commands will not be found", paths.BinDir())
	}
	if db, err := pkgdb.Load(); err != nil {
		r.fail("installed.json is unreadable: %v", err)
	} else {
		for _, p := range db.All() {
			if _, err := os.Stat(p.Dir); err != nil {
				r.warn("%s: install folder %s is missing; reinstall it", p.ID, p.Dir)
				continue
			}
			for _, l := range p.Links {
				if _, err := os.Stat(l); err != nil {
					r.warn("%s: link %s is broken", p.ID, l)
				}
			}
		}
		ui.Note("%d packages installed", len(db.All()))
	}

	ui.Info("configuration")
	path := findConfig(*cfg)
	server := ""
	if path == "" {
		r.warn("no config found (needed to publish or manage the store); looked in %s", strings.Join(configCandidates(), ", "))
	} else {
		r.ok("config %s", path)
		if c, err := builder.LoadConfig(path); err != nil {
			r.fail("config: %v", err)
		} else {
			server = c.Server
			if st, err := os.Stat(c.KeyFile); err != nil {
				r.fail("key file %s: %v", c.KeyFile, err)
			} else {
				if st.Mode().Perm()&0o077 != 0 {
					r.warn("key file %s is readable by others; run chmod 600", c.KeyFile)
				}
				if id, err := proto.LoadIdentity(c.KeyFile); err != nil {
					r.fail("key file: %v", err)
				} else {
					r.ok("signing key %s", id.ID)
				}
			}
		}
	}
	if server == "" {
		server, _ = resolveServer("", *cfg)
	}
	if server != "" {
		checkServerHealth(ctx, r, server)
	}

	if fileExists(filepath.Join("nekpo", builder.MetaFile)) {
		ui.Info("project in %s", mustGetwd())
		checkProject(r)
	}

	fmt.Fprintln(os.Stderr)
	if r.fails > 0 {
		return fmt.Errorf("%d problems found", r.fails)
	}
	if r.warns > 0 {
		ui.OK("no blocking problems, %d warnings", r.warns)
	} else {
		ui.OK("everything looks good")
	}
	return nil
}

func mustGetwd() string {
	d, _ := os.Getwd()
	return d
}

func checkServerHealth(ctx context.Context, r *report, server string) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/api/v1/deps", nil)
	if err != nil {
		r.fail("server %s: %v", server, err)
		return
	}
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.fail("server %s is not reachable: %v", server, err)
		return
	}
	defer resp.Body.Close()
	r.ok("server %s answered in %s", server, ui.Elapsed(time.Since(start)))
	switch resp.StatusCode {
	case http.StatusOK:
		r.ok("dependency API is available")
	case http.StatusNotFound:
		r.warn("the server has no dependency API; update the store to use 'nekpo deps'")
	default:
		r.warn("dependency API answered %d", resp.StatusCode)
	}
	if t, err := http.ParseTime(resp.Header.Get("Date")); err == nil {
		skew := time.Since(t)
		if skew < 0 {
			skew = -skew
		}
		switch {
		case skew > time.Duration(proto.MaxSkew)*time.Second:
			r.fail("your clock is %s off from the server; signed requests will be rejected", ui.Elapsed(skew))
		case skew > 2*time.Minute:
			r.warn("your clock is %s off from the server", ui.Elapsed(skew))
		default:
			r.ok("clock is in sync with the server")
		}
	}
}

func checkProject(r *report) {
	meta, err := builder.LoadMeta(filepath.Join("nekpo", builder.MetaFile))
	if err != nil {
		r.fail("%v", err)
		return
	}
	r.ok("meta.conf: %s with %d artifact rules", meta.App.ID, len(meta.Rules))
	if len(meta.Rules) == 0 {
		r.fail("meta.conf has no [artifact] section")
	}
	if !fileExists(filepath.Join("nekpo", builder.BuildScript)) {
		r.fail("nekpo/build.sh is missing")
	} else {
		r.ok("nekpo/build.sh")
	}
	if meta.ChangelogPath != "" && !fileExists(meta.ChangelogPath) {
		r.warn("changelog %s is listed in meta.conf but missing", meta.ChangelogPath)
	}
	for _, p := range append([]string{meta.IconPath, meta.BannerPath}, meta.ShotPaths...) {
		if p != "" && !fileExists(p) {
			r.warn("%s is listed in meta.conf but missing", p)
		}
	}
	if fileExists(filepath.Join("nekpo", depsconf.ConfName)) {
		f, err := depsconf.Parse(filepath.Join("nekpo", depsconf.ConfName))
		if err != nil {
			r.fail("%v", err)
		} else {
			r.ok("deps.conf: %d dependencies", len(f.Deps))
			if !fileExists(filepath.Join("nekpo", depsconf.LockName)) {
				r.warn("no deps.lock yet; run 'nekpo deps sync' and commit it")
			}
		}
	}
	if raw, err := os.ReadFile(".gitignore"); err != nil || !strings.Contains(string(raw), "nekpo_artifacts") {
		r.warn("nekpo/nekpo_artifacts is not in .gitignore")
	}
}
