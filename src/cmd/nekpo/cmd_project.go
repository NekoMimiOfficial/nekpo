package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"nekpo/internal/builder"
	"nekpo/internal/depsclient"
	"nekpo/internal/paths"
	"nekpo/internal/tmpl"
	"nekpo/internal/ui"
)

func cmdInit(args []string) error {
	fs := newFlags("init")
	force := fs.Bool("force", false, "replace files that already exist")
	server := fs.String("server", "", "server URL suggested for dependencies")
	cfg := fs.String("config", "", "builder config to offer a [repo] entry for")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	dir := "."
	if len(pos) > 1 {
		return errors.New("init takes at most one directory")
	}
	if len(pos) == 1 {
		dir = pos[0]
	}
	srv, _ := resolveServer(*server, *cfg)
	return tmpl.Run(tmpl.WizardOptions{Dir: dir, Force: *force, Server: srv, ConfPath: findConfig(*cfg)})
}

func projectID(dir string) string {
	if m, err := builder.LoadMeta(filepath.Join(dir, builder.NekpoDir, builder.MetaFile)); err == nil {
		return m.App.ID
	}
	return builder.SafeName(filepath.Base(dir))
}

func buildProject(ctx context.Context, dir, serverFlag, cfgPath string, noDeps bool, timeout time.Duration) error {
	if !fileExists(filepath.Join(dir, builder.NekpoDir, builder.BuildScript)) {
		return fmt.Errorf("%s has no nekpo/build.sh; run 'nekpo init' first", dir)
	}
	commit, _ := builder.Git(dir, "rev-parse", "HEAD")
	if !noDeps && fileExists(filepath.Join(dir, builder.NekpoDir, "deps.conf")) {
		srv, insecure := resolveServer(serverFlag, cfgPath)
		if err := depsclient.Sync(ctx, depsclient.SyncOptions{Root: dir, Server: srv, Insecure: insecure}); err != nil {
			return err
		}
	}
	id := projectID(dir)
	logPath := filepath.Join(paths.LogDir(), fmt.Sprintf("build-%s-%s.log", id, time.Now().Format("20060102-150405")))
	if err := builder.RunBuildUI(ctx, "build "+id, dir, builder.BuildEnv(dir, id, commit), logPath, timeout); err != nil {
		return err
	}
	ui.Note("build log: %s", logPath)
	entries, err := os.ReadDir(filepath.Join(dir, builder.NekpoDir, builder.ArtifactsDir))
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() {
			ui.OK("%s (%s)", e.Name(), ui.HumanBytes(info.Size()))
		}
	}
	return nil
}

func cmdBuild(ctx context.Context, args []string) error {
	fs := newFlags("build")
	cfg := fs.String("config", "", "config file (for the dependency server)")
	server := fs.String("server", "", "dependency server URL")
	noDeps := fs.Bool("no-deps", false, "skip pulling dependencies")
	timeout := fs.Duration("timeout", 2*time.Hour, "build timeout")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	dir := "."
	if len(pos) == 1 {
		dir = pos[0]
	} else if len(pos) > 1 {
		return errors.New("build takes at most one directory")
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	return buildProject(ctx, dir, *server, *cfg, *noDeps, *timeout)
}

func cmdPublish(ctx context.Context, args []string, legacy bool) error {
	name := "publish"
	if legacy {
		name = "local"
	}
	fs := newFlags(name)
	cfg := fs.String("config", "", "builder config")
	build := fs.Bool("build", false, "run nekpo/build.sh first")
	dry := fs.Bool("dry", false, "print the manifest instead of uploading")
	var pub *bool
	if legacy {
		pub = fs.Bool("publish", false, "upload the result to the store")
	}
	noDeps := fs.Bool("no-deps", false, "skip pulling dependencies")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		if len(pos) == 0 && !legacy {
			pos = []string{"."}
		} else {
			return fmt.Errorf("%s needs a project directory", name)
		}
	}
	dir, err := filepath.Abs(pos[0])
	if err != nil {
		return err
	}
	upload := !*dry
	if legacy {
		upload = *pub
	}
	var runner *builder.Runner
	if upload || *build {
		if runner, err = newRunner(*cfg); err != nil {
			return err
		}
	}
	if *build {
		if err := buildProject(ctx, dir, "", *cfg, *noDeps, runner.Cfg.BuildTimeout); err != nil {
			return err
		}
	}
	commit, _ := builder.Git(dir, "rev-parse", "HEAD")
	res, err := builder.Prepare(dir, commit)
	if err != nil {
		return err
	}
	app := res.Manifest.App
	if !upload {
		out, _ := json.MarshalIndent(res.Manifest, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	p := ui.Start(fmt.Sprintf("publish %s %s (%d artifacts)", app.ID, app.Version, len(app.Artifacts)))
	body, err := builder.Publish(ctx, runner.Cfg.Server, runner.ID, res)
	p.Finish(err)
	if err != nil {
		return err
	}
	ui.Note("%s", body)
	return nil
}
