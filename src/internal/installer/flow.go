package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"nekpo/internal/archive"
	"nekpo/internal/builder"
	"nekpo/internal/depsclient"
	"nekpo/internal/nlog"
	"nekpo/internal/paths"
	"nekpo/internal/pkgdb"
	"nekpo/internal/proto"
	"nekpo/internal/ui"
	"nekpo/internal/ver"
)

type Spec struct {
	Kind   string
	URL    string
	Ref    string
	Server string
	ID     string
}

type Options struct {
	Yes        bool
	Kind       string
	DepsServer string
	Insecure   bool
}

var ghRe = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

func ParseSpec(arg, ref, server string) (Spec, error) {
	switch {
	case strings.HasPrefix(arg, "gh+"):
		rest := strings.TrimPrefix(arg, "gh+")
		if i := strings.LastIndex(rest, "@"); i > 0 {
			if ref == "" {
				ref = rest[i+1:]
			}
			rest = rest[:i]
		}
		rest = strings.TrimSuffix(rest, ".git")
		if !ghRe.MatchString(rest) {
			return Spec{}, fmt.Errorf("%q: expected gh+AUTHOR/REPO", arg)
		}
		return Spec{Kind: "git", URL: "https://github.com/" + rest + ".git", Ref: ref}, nil
	case strings.HasPrefix(arg, "store:"):
		rest := strings.TrimPrefix(arg, "store:")
		if strings.HasPrefix(rest, "http://") || strings.HasPrefix(rest, "https://") {
			i := strings.LastIndex(rest, "/")
			server, rest = rest[:i], rest[i+1:]
		}
		if !proto.IDRe.MatchString(rest) {
			return Spec{}, fmt.Errorf("%q: expected store:APP-ID or store:https://host/APP-ID", arg)
		}
		if server == "" {
			return Spec{}, errors.New("no store server: pass -server, set NEKPO_SERVER, or use store:https://host/APP-ID")
		}
		return Spec{Kind: "store", Server: strings.TrimRight(server, "/"), ID: rest}, nil
	case strings.Contains(arg, "://"), strings.HasPrefix(arg, "git@"), strings.HasSuffix(arg, ".git"):
		return Spec{Kind: "git", URL: arg, Ref: ref}, nil
	}
	return Spec{}, fmt.Errorf("%q: use gh+AUTHOR/REPO, a git URL, or store:APP-ID", arg)
}

func finish(db *pkgdb.DB, pkg *pkgdb.Package) error {
	old := db.Get(pkg.ID)
	db.Put(pkg)
	if err := db.Save(); err != nil {
		return err
	}
	if old != nil {
		removeStale(old, pkg)
	}
	return nil
}

func Install(ctx context.Context, db *pkgdb.DB, spec Spec, o Options) (*pkgdb.Package, error) {
	if spec.Kind == "store" {
		return installStore(ctx, db, spec, o)
	}
	return installGit(ctx, db, spec, o)
}

func installGit(ctx context.Context, db *pkgdb.DB, spec Spec, o Options) (*pkgdb.Package, error) {
	if _, err := builder.RemoteRef(spec.URL, spec.Ref); err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", spec.URL, err)
	}
	if err := paths.Ensure(paths.SrcDir(), paths.LogDir()); err != nil {
		return nil, err
	}
	dir := filepath.Join(paths.SrcDir(), builder.SafeName(spec.URL))
	if err := builder.CloneUI(ctx, "clone "+spec.URL, spec.URL, spec.Ref, dir, nlog.Writer()); err != nil {
		return nil, err
	}
	commit, err := builder.Git(dir, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	meta, err := builder.LoadMeta(filepath.Join(dir, builder.NekpoDir, builder.MetaFile))
	if err != nil {
		return nil, fmt.Errorf("%s is not a nekpo project: %w", spec.URL, err)
	}
	ui.Info("%s (%s) at %s", meta.App.Name, meta.App.ID, commit[:12])
	if !o.Yes && !ui.Confirm("This runs nekpo/build.sh from that repository on this machine. Continue?", false) {
		return nil, errors.New("cancelled")
	}
	if err := depsclient.Sync(ctx, depsclient.SyncOptions{Root: dir, Server: o.DepsServer, Insecure: o.Insecure}); err != nil {
		return nil, err
	}
	logPath := filepath.Join(paths.LogDir(), fmt.Sprintf("install-%s-%s.build.log", meta.App.ID, time.Now().Format("20060102-150405")))
	if err := builder.RunBuildUI(ctx, "build "+meta.App.ID, dir, builder.BuildEnv(dir, meta.App.ID, commit), logPath, 2*time.Hour); err != nil {
		return nil, err
	}
	res, err := builder.Prepare(dir, commit)
	if err != nil {
		return nil, err
	}
	app := res.Manifest.App
	art, err := Pick(app.Artifacts, o.Kind)
	if err != nil {
		return nil, err
	}
	file := filepath.Join(dir, builder.NekpoDir, builder.ArtifactsDir, art.Name)
	pkg, err := place(placeIn{ID: app.ID, Name: app.Name, Version: app.Version, File: file, Art: art, Meta: readInstallMeta(dir)})
	if err != nil {
		return nil, err
	}
	pkg.Commit = commit
	pkg.Source = pkgdb.Source{Kind: "git", URL: spec.URL, Ref: spec.Ref}
	if err := finish(db, pkg); err != nil {
		return nil, err
	}
	os.RemoveAll(dir)
	return pkg, nil
}

func httpClient() *http.Client { return &http.Client{Timeout: 0} }

func fetchApp(ctx context.Context, server, id string) (*proto.App, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/api/v1/apps/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("the store has no app %q", id)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("store said %d", resp.StatusCode)
	}
	var app proto.App
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&app); err != nil {
		return nil, err
	}
	return &app, nil
}

func installStore(ctx context.Context, db *pkgdb.DB, spec Spec, o Options) (*pkgdb.Package, error) {
	app, err := fetchApp(ctx, spec.Server, spec.ID)
	if err != nil {
		return nil, err
	}
	art, err := Pick(app.Artifacts, o.Kind)
	if err != nil {
		return nil, err
	}
	ui.Info("%s %s from %s", app.Name, app.Version, spec.Server)
	if err := paths.Ensure(paths.CacheDir()); err != nil {
		return nil, err
	}
	file := filepath.Join(paths.CacheDir(), art.Name)
	link := spec.Server + "/dl/" + url.PathEscape(app.ID) + "/" + url.PathEscape(app.Version) + "/" + url.PathEscape(art.Name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	p := ui.Start("download " + art.Name)
	dl := func() error {
		resp, err := httpClient().Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("store said %d for the download", resp.StatusCode)
		}
		out, err := os.Create(file)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, &ui.Reader{R: resp.Body, Total: art.Size, P: p})
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		return err
	}
	err = dl()
	p.Finish(err)
	if err != nil {
		os.Remove(file)
		return nil, err
	}
	defer os.Remove(file)
	sum, size, err := archive.SumFile(file)
	if err != nil {
		return nil, err
	}
	if sum != art.SHA256 || size != art.Size {
		return nil, errors.New("the download does not match the checksum published by the store")
	}
	ui.OK("checksum verified")
	pkg, err := place(placeIn{ID: app.ID, Name: app.Name, Version: app.Version, File: file, Art: art})
	if err != nil {
		return nil, err
	}
	pkg.Source = pkgdb.Source{Kind: "store", Server: spec.Server}
	if err := finish(db, pkg); err != nil {
		return nil, err
	}
	return pkg, nil
}

type Status struct {
	Pkg      *pkgdb.Package
	Latest   string
	Outdated bool
	Pinned   bool
	Err      error
}

func Check(ctx context.Context, db *pkgdb.DB, ids []string) []Status {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []Status
	for _, p := range db.All() {
		if len(want) > 0 && !want[p.ID] {
			continue
		}
		st := Status{Pkg: p}
		switch p.Source.Kind {
		case "git":
			head, err := builder.RemoteRef(p.Source.URL, p.Source.Ref)
			if err != nil {
				st.Err = err
				break
			}
			st.Latest = head
			if len(head) > 12 {
				st.Latest = head[:12]
			}
			st.Outdated = head != p.Commit
			st.Pinned = p.Source.Ref != ""
		case "store":
			app, err := fetchApp(ctx, p.Source.Server, p.ID)
			if err != nil {
				st.Err = err
				break
			}
			st.Latest = app.Version
			st.Outdated = ver.Compare(app.Version, p.Version) > 0
		default:
			st.Err = errors.New("unknown source")
		}
		out = append(out, st)
	}
	return out
}

func SpecOf(p *pkgdb.Package) Spec {
	if p.Source.Kind == "store" {
		return Spec{Kind: "store", Server: p.Source.Server, ID: p.ID}
	}
	return Spec{Kind: "git", URL: p.Source.URL, Ref: p.Source.Ref}
}
