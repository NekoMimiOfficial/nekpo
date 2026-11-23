package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nekpo/internal/depsclient"
	"nekpo/internal/installer"
	"nekpo/internal/paths"
	"nekpo/internal/pkgdb"
	"nekpo/internal/ui"
)

func binOnPath() bool {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.Clean(p) == filepath.Clean(paths.BinDir()) {
			return true
		}
	}
	return false
}

func cmdInstall(ctx context.Context, args []string) error {
	fs := newFlags("install")
	yes := fs.Bool("yes", false, "do not ask before running a project's build script")
	server := fs.String("server", "", "store server for store:APP-ID")
	ref := fs.String("ref", "", "branch or tag to install from a git URL")
	kind := fs.String("kind", "", "artifact kind to install (tarball or appimage)")
	cfg := fs.String("config", "", "config file")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("install needs one of: gh+AUTHOR/REPO, a git URL, store:APP-ID")
	}
	srv, insecure := resolveServer(*server, *cfg)
	spec, err := installer.ParseSpec(pos[0], *ref, srv)
	if err != nil {
		return err
	}
	if spec.Kind == "store" {
		if err := depsclient.CheckServer(spec.Server, insecure); err != nil {
			return err
		}
	}
	db, err := pkgdb.Load()
	if err != nil {
		return err
	}
	pkg, err := installer.Install(ctx, db, spec, installer.Options{Yes: *yes, Kind: *kind, DepsServer: srv, Insecure: insecure})
	if err != nil {
		return err
	}
	ui.OK("installed %s %s", pkg.Name, pkg.Version)
	ui.Note("location: %s", pkg.Dir)
	for _, l := range pkg.Links {
		ui.Note("command: %s", l)
	}
	if len(pkg.Links) > 0 && !binOnPath() {
		ui.Warn("%s is not in your PATH; add it to your shell profile to run the installed commands", paths.BinDir())
	}
	return nil
}

func cmdUninstall(args []string) error {
	fs := newFlags("uninstall")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("uninstall needs at least one package id")
	}
	db, err := pkgdb.Load()
	if err != nil {
		return err
	}
	var failed []string
	for _, id := range pos {
		if err := installer.Uninstall(db, id); err != nil {
			ui.Fail("%v", err)
			failed = append(failed, id)
			continue
		}
		ui.OK("removed %s", id)
	}
	if len(failed) > 0 {
		return fmt.Errorf("could not remove: %s", strings.Join(failed, ", "))
	}
	return nil
}

func sourceText(p *pkgdb.Package) string {
	switch p.Source.Kind {
	case "store":
		return "store " + p.Source.Server
	case "git":
		s := p.Source.URL
		if p.Source.Ref != "" {
			s += " @" + p.Source.Ref
		}
		return s
	}
	return p.Source.Kind
}

func cmdList(args []string) error {
	fs := newFlags("list")
	quiet := fs.Bool("q", false, "print only package ids")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	db, err := pkgdb.Load()
	if err != nil {
		return err
	}
	all := db.All()
	if *quiet {
		for _, p := range all {
			fmt.Println(p.ID)
		}
		return nil
	}
	if len(all) == 0 {
		ui.Info("nothing installed yet")
		return nil
	}
	var rows [][]string
	for _, p := range all {
		rows = append(rows, []string{p.ID, p.Version, time.Unix(p.Installed, 0).Format("2006-01-02"), sourceText(p)})
	}
	ui.Table([]string{"ID", "VERSION", "INSTALLED", "SOURCE"}, rows)
	return nil
}

func statusCell(st installer.Status) string {
	switch {
	case st.Err != nil:
		return ui.Red("check failed: " + st.Err.Error())
	case st.Outdated && st.Pinned:
		return ui.Yellow("changed upstream (pinned ref)")
	case st.Outdated:
		return ui.Yellow("update available")
	}
	return ui.Green("up to date")
}

func cmdOutdated(ctx context.Context, args []string) error {
	fs := newFlags("outdated")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	db, err := pkgdb.Load()
	if err != nil {
		return err
	}
	sts := installer.Check(ctx, db, pos)
	if len(sts) == 0 {
		ui.Info("nothing installed yet")
		return nil
	}
	var rows [][]string
	for _, st := range sts {
		have := st.Pkg.Version
		if st.Pkg.Commit != "" {
			c := st.Pkg.Commit
			if len(c) > 7 {
				c = c[:7]
			}
			have += " (" + c + ")"
		}
		rows = append(rows, []string{st.Pkg.ID, have, st.Latest, statusCell(st)})
	}
	ui.Table([]string{"ID", "INSTALLED", "LATEST", "STATUS"}, rows)
	return nil
}

func cmdUpdate(ctx context.Context, args []string) error {
	fs := newFlags("update")
	yes := fs.Bool("yes", false, "do not ask before running build scripts")
	cfg := fs.String("config", "", "config file")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	db, err := pkgdb.Load()
	if err != nil {
		return err
	}
	srv, insecure := resolveServer("", *cfg)
	var todo []installer.Status
	for _, st := range installer.Check(ctx, db, pos) {
		if st.Err != nil {
			ui.Warn("%s: %v", st.Pkg.ID, st.Err)
			continue
		}
		if st.Outdated {
			todo = append(todo, st)
		}
	}
	if len(todo) == 0 {
		ui.OK("everything is up to date")
		return nil
	}
	failed := 0
	for _, st := range todo {
		ui.Info("updating %s", st.Pkg.ID)
		spec := installer.SpecOf(st.Pkg)
		if spec.Kind == "store" {
			if err := depsclient.CheckServer(spec.Server, insecure); err != nil {
				ui.Fail("%s: %v", st.Pkg.ID, err)
				failed++
				continue
			}
		}
		pkg, err := installer.Install(ctx, db, spec, installer.Options{Yes: *yes, DepsServer: srv, Insecure: insecure})
		if err != nil {
			ui.Fail("%s: %v", st.Pkg.ID, err)
			failed++
			continue
		}
		ui.OK("%s is now %s", pkg.ID, pkg.Version)
	}
	if failed > 0 {
		return fmt.Errorf("%d updates failed", failed)
	}
	return nil
}
