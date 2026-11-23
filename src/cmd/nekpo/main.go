package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"nekpo/internal/builder"
	"nekpo/internal/conf"
	"nekpo/internal/depsclient"
	"nekpo/internal/nlog"
	"nekpo/internal/paths"
	"nekpo/internal/ui"
)

const Version = "1.1.0"

var errHelp = errors.New("help")

const usageText = `nekpo [` + Version + `]  build, publish and install projects

usage: nekpo <command> [flags]

project
  init [dir]                         interactive project setup (meta, build script, CMake, packaging)
  build [dir]                        pull dependencies and run nekpo/build.sh
  publish [-build] [-dry] [dir]      prepare the project and upload it to the store
  release patch|minor|major          bump the version, update the changelog, commit and tag

packages
  install <gh+AUTHOR/REPO | git-url | store:APP-ID>
  uninstall <id>...
  list [-q]                          installed packages
  outdated                           check installed packages for newer versions
  update [id...]                     update outdated packages

dependencies
  deps push|pull|sync|lock|list|info|remove

store management
  app list|versions|edit|asset|rm-asset|rm-artifact|rollback|unpublish

ci
  run [-config f]                    watch the repositories in builder.conf
  once [-config f] [-force] [name...]
  keygen [-id name] [-out file]

other
  doctor                             check the environment
  completion bash|zsh|fish           print a shell completion script
  version                            print the version

run 'nekpo <command> -h' for the flags of a command
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageText)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version", "--version", "-version", "-v":
		fmt.Println("nekpo " + Version)
		return 0
	case "help", "--help", "-help", "-h":
		fmt.Print(usageText)
		return 0
	case "completion":
		if err := cmdCompletion(rest); err != nil {
			ui.Fail("%v", err)
			return 2
		}
		return 0
	}
	if cmd != "list" && cmd != "ls" {
		nlog.Open(cmd)
	}
	defer nlog.Close()
	nlog.Printf("nekpo %s %s", Version, strings.Join(args, " "))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch cmd {
	case "init":
		err = cmdInit(rest)
	case "build":
		err = cmdBuild(ctx, rest)
	case "publish":
		err = cmdPublish(ctx, rest, false)
	case "local":
		err = cmdPublish(ctx, rest, true)
	case "release":
		err = cmdRelease(ctx, rest)
	case "install", "add":
		err = cmdInstall(ctx, rest)
	case "uninstall", "remove":
		err = cmdUninstall(rest)
	case "list", "ls":
		err = cmdList(rest)
	case "outdated":
		err = cmdOutdated(ctx, rest)
	case "update", "upgrade":
		err = cmdUpdate(ctx, rest)
	case "deps":
		err = cmdDeps(ctx, rest)
	case "app":
		err = cmdApp(ctx, rest)
	case "doctor":
		err = cmdDoctor(ctx, rest)
	case "run":
		err = cmdRunLoop(ctx, rest)
	case "once":
		err = cmdOnce(ctx, rest)
	case "keygen":
		err = cmdKeygen(rest)
	case "unpublish":
		err = cmdApp(ctx, append([]string{"unpublish"}, rest...))
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usageText)
		return 2
	}
	if err != nil {
		if errors.Is(err, errHelp) {
			return 0
		}
		ui.Fail("%v", err)
		if p := nlog.Path(); p != "" {
			ui.Note("log: %s", p)
		}
		return 1
	}
	return 0
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, errHelp
			}
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func configCandidates() []string {
	c := []string{"builder.conf"}
	if dir, err := os.UserConfigDir(); err == nil {
		c = append(c, filepath.Join(dir, "nekpo", "builder.conf"))
	}
	c = append(c, filepath.Join(paths.ConfigDir(), "builder.conf"), paths.ClientConf())
	seen := map[string]bool{}
	var out []string
	for _, p := range c {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func findConfig(explicit string) string {
	if explicit != "" {
		return explicit
	}
	for _, c := range configCandidates() {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

type settings struct {
	Path     string
	Server   string
	Insecure bool
}

func loadSettings(explicit string) settings {
	p := findConfig(explicit)
	if p == "" {
		return settings{}
	}
	f, err := conf.Load(p)
	if err != nil {
		return settings{Path: p}
	}
	return settings{Path: p, Server: strings.TrimRight(f.Root.Get("server"), "/"), Insecure: f.Root.Bool("allow_insecure", false)}
}

func resolveServer(flagVal, cfgPath string) (string, bool) {
	st := loadSettings(cfgPath)
	switch {
	case flagVal != "":
		return strings.TrimRight(flagVal, "/"), st.Insecure
	case os.Getenv("NEKPO_SERVER") != "":
		return strings.TrimRight(os.Getenv("NEKPO_SERVER"), "/"), st.Insecure
	}
	return st.Server, st.Insecure
}

func needServer(flagVal, cfgPath string) (string, error) {
	s, insecure := resolveServer(flagVal, cfgPath)
	if s == "" {
		return "", errors.New("no server: use -server, set NEKPO_SERVER, or put server = https://... in builder.conf or ~/.config/nekpo/nekpo.conf")
	}
	if err := depsclient.CheckServer(s, insecure); err != nil {
		return "", err
	}
	return s, nil
}

func newRunner(explicit string) (*builder.Runner, error) {
	p := findConfig(explicit)
	if p == "" {
		return nil, fmt.Errorf("no builder config found, looked in: %s (use -config)", strings.Join(configCandidates(), ", "))
	}
	cfg, err := builder.LoadConfig(p)
	if err != nil {
		return nil, err
	}
	r, err := builder.NewRunner(cfg)
	if err != nil {
		return nil, err
	}
	ui.Note("using config %s", p)
	return r, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
