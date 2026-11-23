package builder

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func remoteHead(url, branch string) (string, error) {
	ref := "HEAD"
	if branch != "" {
		ref = "refs/heads/" + branch
	}
	out, err := git("", "ls-remote", url, ref)
	if err != nil {
		return "", fmt.Errorf("git ls-remote %s: %w", url, err)
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", fmt.Errorf("git ls-remote %s: ref %s not found", url, ref)
	}
	return fields[0], nil
}

func clone(url, branch, dest string, logw io.Writer) error {
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	try := func(extra ...string) error {
		args := append([]string{"clone", "--quiet"}, extra...)
		if branch != "" {
			args = append(args, "--branch", branch)
		}
		args = append(args, url, dest)
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		cmd.Stdout, cmd.Stderr = logw, logw
		return cmd.Run()
	}
	if err := try("--filter=blob:none"); err == nil {
		return nil
	}
	os.RemoveAll(dest)
	return try()
}

func tagDate(dir, version string) string {
	for _, tag := range []string{"v" + version, version} {
		if out, err := git(dir, "log", "-1", "--format=%cs", "refs/tags/"+tag); err == nil && out != "" {
			return out
		}
	}
	return ""
}
