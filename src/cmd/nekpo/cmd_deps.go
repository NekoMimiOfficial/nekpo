package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nekpo/internal/archive"
	"nekpo/internal/depsclient"
	"nekpo/internal/proto"
	"nekpo/internal/ui"
)

const depsUsage = `usage: nekpo deps <command>

  push [-lang cpp] [-arch noarch] [-replace] [-note text] <name>:<version> <file|dir>
  pull [-lang cpp] [-arch noarch] [-o dir] [-force] <name>[:version]
  sync [-force] [-update] [-no-lock] [dir]    install everything in nekpo/deps.conf
  lock [dir]                                  refresh nekpo/deps.lock without downloading
  list [-lang l] [-arch a] [name]
  info [-lang cpp] [-arch noarch] <name>[:version]
  remove [-lang cpp] [-arch noarch] [-yes] <name>:<version>
`

func splitSpec(spec string) (string, string) {
	name, version, _ := strings.Cut(spec, ":")
	return name, version
}

func cmdDeps(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, depsUsage)
		return errors.New("deps needs a command")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "push":
		return depsPush(ctx, rest)
	case "pull":
		return depsPull(ctx, rest)
	case "sync":
		return depsSync(ctx, rest, false)
	case "lock":
		return depsSync(ctx, rest, true)
	case "list", "ls":
		return depsList(ctx, rest)
	case "info":
		return depsInfo(ctx, rest)
	case "remove", "rm":
		return depsRemove(ctx, rest)
	case "help", "-h", "--help":
		fmt.Print(depsUsage)
		return nil
	}
	fmt.Fprint(os.Stderr, depsUsage)
	return fmt.Errorf("unknown deps command %q", sub)
}

func depsPush(ctx context.Context, args []string) error {
	fs := newFlags("deps push")
	lang := fs.String("lang", "cpp", "language of the dependency")
	arch := fs.String("arch", "noarch", "architecture (noarch, amd64, arm64, ...)")
	replace := fs.Bool("replace", false, "overwrite an existing version")
	note := fs.String("note", "", "short note stored with the version")
	cfg := fs.String("config", "", "builder config")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return errors.New("usage: nekpo deps push <name>:<version> <file|dir>")
	}
	name, version := splitSpec(pos[0])
	a := proto.NormalizeArch(*arch)
	if err := proto.ValidDepKey(*lang, a, name); err != nil {
		return err
	}
	if !proto.DepVersionRe.MatchString(version) || version == "latest" {
		return errors.New("a concrete version is required: <name>:<version>")
	}
	st, err := os.Stat(pos[1])
	if err != nil {
		return err
	}
	file := pos[1]
	if st.IsDir() {
		tmp, err := os.MkdirTemp("", "nekpo-dep-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		file = filepath.Join(tmp, name+"-"+version+".tar.gz")
		p := ui.Start("pack " + pos[1])
		err = archive.Create(pos[1], file)
		p.Finish(err)
		if err != nil {
			return err
		}
	} else if archive.Kind(file) == "" {
		return errors.New("dependencies must be .tar.gz, .tgz, .tar.bz2, .tar or .zip files, or a directory")
	}
	runner, err := newRunner(*cfg)
	if err != nil {
		return err
	}
	p := ui.Start(fmt.Sprintf("push %s %s (%s, %s)", name, version, *lang, a))
	msg, err := depsclient.New(runner.Cfg.Server).Push(ctx, runner.ID, *lang, a, name, version, file, *replace, *note, p)
	p.Finish(err)
	if err != nil {
		return err
	}
	ui.Note("%s", msg)
	return nil
}

func depsPull(ctx context.Context, args []string) error {
	fs := newFlags("deps pull")
	lang := fs.String("lang", "cpp", "language of the dependency")
	arch := fs.String("arch", "noarch", "architecture")
	out := fs.String("o", "", "destination folder (default third_party/<name> for cpp, deps/<lang>/<name> otherwise)")
	force := fs.Bool("force", false, "replace an existing folder")
	server := fs.String("server", "", "server URL")
	cfg := fs.String("config", "", "config file")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: nekpo deps pull <name>[:version]")
	}
	name, version := splitSpec(pos[0])
	a := proto.NormalizeArch(*arch)
	if err := proto.ValidDepKey(*lang, a, name); err != nil {
		return err
	}
	srv, err := needServer(*server, *cfg)
	if err != nil {
		return err
	}
	c := depsclient.New(srv)
	info, err := c.Info(ctx, *lang, a, name, version)
	if err != nil {
		return err
	}
	dest := *out
	if dest == "" {
		if *lang == "cpp" {
			dest = filepath.Join("third_party", name)
		} else {
			dest = filepath.Join("deps", *lang, name)
		}
	}
	tmpRoot := ".nekpo-deps-tmp"
	defer os.RemoveAll(tmpRoot)
	fetched, err := depsclient.Fetch(ctx, c, info, dest, tmpRoot, *force)
	if err != nil {
		return err
	}
	if fetched {
		ui.OK("%s %s is in %s", info.Name, info.Version, dest)
	} else {
		ui.Note("%s already has %s", dest, info.Name)
	}
	return nil
}

func depsSync(ctx context.Context, args []string, lockOnly bool) error {
	name := "deps sync"
	if lockOnly {
		name = "deps lock"
	}
	fs := newFlags(name)
	force := fs.Bool("force", false, "replace folders that already exist")
	update := fs.Bool("update", false, "ignore deps.lock and resolve the newest versions")
	noLock := fs.Bool("no-lock", false, "do not write deps.lock")
	server := fs.String("server", "", "server URL (deps.conf can set its own)")
	cfg := fs.String("config", "", "config file")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	dir := "."
	if len(pos) == 1 {
		dir = pos[0]
	} else if len(pos) > 1 {
		return errors.New("one project directory at most")
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	if !fileExists(filepath.Join(dir, "nekpo", "deps.conf")) {
		return fmt.Errorf("%s has no nekpo/deps.conf", dir)
	}
	srv, insecure := resolveServer(*server, *cfg)
	return depsclient.Sync(ctx, depsclient.SyncOptions{Root: dir, Server: srv, Insecure: insecure, Force: *force, Update: *update || lockOnly, NoLock: *noLock, LockOnly: lockOnly})
}

func depsList(ctx context.Context, args []string) error {
	fs := newFlags("deps list")
	lang := fs.String("lang", "", "only this language")
	arch := fs.String("arch", "", "only this architecture")
	server := fs.String("server", "", "server URL")
	cfg := fs.String("config", "", "config file")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	srv, err := needServer(*server, *cfg)
	if err != nil {
		return err
	}
	name := ""
	if len(pos) > 0 {
		name = pos[0]
	}
	a := *arch
	if a != "" {
		a = proto.NormalizeArch(a)
	}
	list, err := depsclient.New(srv).List(ctx, *lang, a, name)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		ui.Info("no dependencies on the server match")
		return nil
	}
	var rows [][]string
	for _, d := range list {
		v := d.Version
		if d.Latest {
			v += " (latest)"
		}
		rows = append(rows, []string{d.Lang, d.Arch, d.Name, v, ui.HumanBytes(d.Size), time.Unix(d.Pushed, 0).Format("2006-01-02")})
	}
	ui.Table([]string{"LANG", "ARCH", "NAME", "VERSION", "SIZE", "PUSHED"}, rows)
	return nil
}

func depsInfo(ctx context.Context, args []string) error {
	fs := newFlags("deps info")
	lang := fs.String("lang", "cpp", "language")
	arch := fs.String("arch", "noarch", "architecture")
	server := fs.String("server", "", "server URL")
	cfg := fs.String("config", "", "config file")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: nekpo deps info <name>[:version]")
	}
	srv, err := needServer(*server, *cfg)
	if err != nil {
		return err
	}
	name, version := splitSpec(pos[0])
	d, err := depsclient.New(srv).Info(ctx, *lang, proto.NormalizeArch(*arch), name, version)
	if err != nil {
		return err
	}
	fmt.Printf("name     %s\nversion  %s\nlanguage %s\narch     %s\nfile     %s\nsize     %s\nsha256   %s\npushed   %s by %s\n", d.Name, d.Version, d.Lang, d.Arch, d.File, ui.HumanBytes(d.Size), d.SHA256, time.Unix(d.Pushed, 0).Format(time.RFC3339), d.Builder)
	if d.Note != "" {
		fmt.Printf("note     %s\n", d.Note)
	}
	return nil
}

func depsRemove(ctx context.Context, args []string) error {
	fs := newFlags("deps remove")
	lang := fs.String("lang", "cpp", "language")
	arch := fs.String("arch", "noarch", "architecture")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	cfg := fs.String("config", "", "builder config")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: nekpo deps remove <name>:<version>")
	}
	name, version := splitSpec(pos[0])
	a := proto.NormalizeArch(*arch)
	if err := proto.ValidDepKey(*lang, a, name); err != nil {
		return err
	}
	if version == "" || version == "latest" {
		return errors.New("a concrete version is required: <name>:<version>")
	}
	if !*yes && !ui.Confirm(fmt.Sprintf("Delete %s %s (%s, %s) from the server?", name, version, *lang, a), false) {
		return errors.New("cancelled")
	}
	runner, err := newRunner(*cfg)
	if err != nil {
		return err
	}
	if _, err := depsclient.New(runner.Cfg.Server).Remove(ctx, runner.ID, *lang, a, name, version); err != nil {
		return err
	}
	ui.OK("removed %s %s", name, version)
	return nil
}
