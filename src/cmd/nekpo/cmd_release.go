package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"nekpo/internal/builder"
	"nekpo/internal/ui"
	"nekpo/internal/ver"
)

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return text, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, text)
	}
	return text, nil
}

var metaVersionRe = regexp.MustCompile(`(?m)^(\s*version\s*=\s*).*$`)

func cmdRelease(ctx context.Context, args []string) error {
	fs := newFlags("release")
	noGit := fs.Bool("no-git", false, "only edit the files; do not commit or tag")
	publish := fs.Bool("publish", false, "build and publish after tagging")
	dry := fs.Bool("dry", false, "show what would change and stop")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	dirFlag := fs.String("dir", ".", "project directory")
	cfg := fs.String("config", "", "builder config (for -publish)")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: nekpo release patch|minor|major [flags]")
	}
	dir, err := filepath.Abs(*dirFlag)
	if err != nil {
		return err
	}
	meta, err := builder.LoadMeta(filepath.Join(dir, builder.NekpoDir, builder.MetaFile))
	if err != nil {
		return err
	}
	cur := builder.ResolveVersion(dir, meta)
	if cur == "" {
		return errors.New("cannot tell the current version; set version in meta.conf or add a changelog heading")
	}
	next, err := ver.Bump(cur, pos[0])
	if err != nil {
		return err
	}
	clPath := meta.ChangelogPath
	if clPath == "" {
		clPath = "changelog.md"
	}
	clFile := filepath.Join(dir, filepath.FromSlash(clPath))
	var lines []string
	if raw, err := os.ReadFile(clFile); err == nil {
		lines = strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	}
	hashes, firstHeader := "#", len(lines)
	for i, l := range lines {
		if m := builder.HeaderRe().FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			hashes, firstHeader = m[1], i
			break
		}
	}
	isGit := fileExists(filepath.Join(dir, ".git"))
	lastTag := ""
	if isGit {
		if t, err := runGit(dir, "describe", "--tags", "--abbrev=0"); err == nil {
			lastTag = t
		}
	}
	prefix := "v"
	if lastTag != "" && !strings.HasPrefix(lastTag, "v") {
		prefix = ""
	}
	tag := prefix + next
	var subjects []string
	if isGit {
		rng := []string{"log", "--no-merges", "--format=%s", "-n", "50"}
		if lastTag != "" {
			rng = append(rng, lastTag+"..HEAD")
		}
		if out, err := runGit(dir, rng...); err == nil {
			for _, s := range strings.Split(out, "\n") {
				if s = strings.TrimSpace(s); s != "" && !strings.HasPrefix(s, "Release ") {
					subjects = append(subjects, s)
				}
			}
		}
		if _, err := runGit(dir, "rev-parse", "-q", "--verify", "refs/tags/"+tag); err == nil {
			return fmt.Errorf("tag %s already exists", tag)
		}
	}
	section := []string{hashes + " v" + next, ""}
	if len(subjects) == 0 {
		section = append(section, "- Release "+next)
	}
	for _, s := range subjects {
		section = append(section, "- "+s)
	}
	section = append(section, "")
	ui.Info("%s -> %s", cur, next)
	for _, l := range section {
		ui.Note("%s", l)
	}
	if *dry {
		return nil
	}
	if isGit && !*noGit {
		if out, err := runGit(dir, "status", "--porcelain"); err == nil && out != "" && !*yes {
			if !ui.Confirm("The working tree has uncommitted changes (they stay out of the release commit). Continue?", false) {
				return errors.New("cancelled")
			}
		}
	}
	if !*yes && !ui.Confirm(fmt.Sprintf("Write %s%s?", clPath, map[bool]string{true: ", commit and tag " + tag, false: ""}[isGit && !*noGit]), true) {
		return errors.New("cancelled")
	}
	merged := append(append(append([]string{}, lines[:firstHeader]...), section...), lines[firstHeader:]...)
	if err := os.MkdirAll(filepath.Dir(clFile), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(clFile, []byte(strings.Join(merged, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	ui.OK("updated %s", clPath)
	metaFile := filepath.Join(dir, builder.NekpoDir, builder.MetaFile)
	metaChanged := false
	if v := strings.TrimSpace(meta.VersionSpec); v != "" && !strings.EqualFold(v, "auto") {
		raw, err := os.ReadFile(metaFile)
		if err != nil {
			return err
		}
		loc := metaVersionRe.FindSubmatchIndex(raw)
		if loc == nil {
			return errors.New("version line not found in meta.conf")
		}
		updated := append(append(append([]byte{}, raw[:loc[3]]...), []byte(next)...), raw[loc[1]:]...)
		if err := os.WriteFile(metaFile, updated, 0o644); err != nil {
			return err
		}
		metaChanged = true
		ui.OK("updated version in meta.conf")
	}
	if isGit && !*noGit {
		files := []string{clPath}
		if metaChanged {
			files = append(files, filepath.ToSlash(filepath.Join(builder.NekpoDir, builder.MetaFile)))
		}
		if _, err := runGit(dir, append([]string{"add", "--"}, files...)...); err != nil {
			return err
		}
		if _, err := runGit(dir, append([]string{"commit", "-m", "Release " + tag, "--"}, files...)...); err != nil {
			return err
		}
		if _, err := runGit(dir, "tag", tag); err != nil {
			return err
		}
		ui.OK("committed and tagged %s (push with: git push && git push origin %s)", tag, tag)
	}
	if *publish {
		return cmdPublish(ctx, []string{"-build", "-config", *cfg, dir}, false)
	}
	return nil
}
