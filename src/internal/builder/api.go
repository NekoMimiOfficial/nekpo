package builder

import (
	"fmt"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"io"

	"nekpo/internal/ui"
)

func Git(dir string, args ...string) (string, error) { return git(dir, args...) }

func RemoteHead(url, ref string) (string, error) { return remoteHead(url, ref) }

func ResolveVersion(dir string, m *Meta) string { return resolveVersion(dir, m) }

func HeaderRe() *regexp.Regexp { return headerRe }

func RunBuildUI(ctx context.Context, label, dir string, env []string, logPath string, timeout time.Duration) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	script := filepath.Join(dir, NekpoDir, BuildScript)
	if _, err := os.Stat(script); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(dir, NekpoDir, ArtifactsDir)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	if logPath, err = filepath.Abs(logPath); err != nil {
		return err
	}
	logf, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logf.Close()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 10 * time.Second
	if err := ui.RunCmd(label, cmd, logf, ui.Generic); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &BuildError{Msg: "build timed out after " + timeout.String(), Log: logPath}
		}
		return &BuildError{Msg: "build failed: " + err.Error(), Log: logPath}
	}
	return nil
}

type BuildError struct {
	Msg string
	Log string
}

func (e *BuildError) Error() string { return e.Msg + " (full log: " + e.Log + ")" }

func CloneUI(ctx context.Context, label, url, ref, dest string, logw io.Writer) error {
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	try := func(extra ...string) error {
		args := append([]string{"clone", "--progress"}, extra...)
		if ref != "" {
			args = append(args, "--branch", ref)
		}
		args = append(args, url, dest)
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		return ui.RunCmd(label, cmd, logw, ui.Generic)
	}
	if err := try("--filter=blob:none"); err == nil {
		return nil
	}
	os.RemoveAll(dest)
	return try()
}

func SafeName(s string) string {
	return strings.Trim(nameBad.ReplaceAllString(s, "_"), "_")
}

func RemoteRef(url, ref string) (string, error) {
	if ref == "" {
		return remoteHead(url, "")
	}
	out, err := git("", "ls-remote", url, "refs/heads/"+ref, "refs/tags/"+ref, "refs/tags/"+ref+"^{}")
	if err != nil {
		return "", err
	}
	first, peeled := "", ""
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if strings.HasSuffix(f[1], "^{}") {
			peeled = f[0]
		} else if first == "" {
			first = f[0]
		}
	}
	if peeled != "" {
		return peeled, nil
	}
	if first == "" {
		return "", fmt.Errorf("ref %q not found in %s", ref, url)
	}
	return first, nil
}
