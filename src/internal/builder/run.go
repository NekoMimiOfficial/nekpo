package builder

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"nekpo/internal/proto"
)

type Runner struct {
	Cfg   *Config
	ID    proto.Identity
	State *State
}

func NewRunner(cfg *Config) (*Runner, error) {
	id, err := proto.LoadIdentity(cfg.KeyFile)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.WorkDir, 0o755); err != nil {
		return nil, err
	}
	return &Runner{Cfg: cfg, ID: id, State: LoadState(cfg.WorkDir)}, nil
}

var nameBad = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func RunBuild(ctx context.Context, dir string, env []string, logPath string, timeout time.Duration) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	script := filepath.Join(dir, NekpoDir, BuildScript)
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("%s not found", filepath.Join(NekpoDir, BuildScript))
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
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("build timed out after %s", timeout)
		}
		return fmt.Errorf("build failed: %w", err)
	}
	return nil
}

func tail(path string, n int) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func BuildEnv(dir, name, commit string) []string {
	return []string{"NEKPO=1", "NEKPO_NAME=" + name, "NEKPO_COMMIT=" + commit, "NEKPO_ARTIFACTS=" + filepath.Join(dir, NekpoDir, ArtifactsDir)}
}

func (r *Runner) Process(ctx context.Context, repo Repo, force bool) error {
	st := r.State.Get(repo.Name)
	head, err := remoteHead(repo.URL, repo.Branch)
	if err != nil {
		return err
	}
	if !force {
		if head == st.Built {
			return nil
		}
		if head == st.Failed && time.Since(time.Unix(st.FailedAt, 0)) < r.Cfg.RetryAfter {
			return nil
		}
	}
	log.Printf("[%s] new commit %.10s, building", repo.Name, head)
	err = r.buildAndPublish(ctx, repo)
	if err != nil {
		st.Failed, st.FailedAt = head, time.Now().Unix()
	} else {
		st.Built, st.Failed, st.FailedAt = head, "", 0
	}
	if serr := r.State.Save(); serr != nil {
		log.Printf("[%s] saving state: %v", repo.Name, serr)
	}
	return err
}

func (r *Runner) buildAndPublish(ctx context.Context, repo Repo) error {
	safe := nameBad.ReplaceAllString(repo.Name, "_")
	dest := filepath.Join(r.Cfg.WorkDir, "checkouts", safe)
	logPath := filepath.Join(r.Cfg.WorkDir, "logs", safe+".log")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := clone(repo.URL, repo.Branch, dest, os.Stderr); err != nil {
		return fmt.Errorf("clone: %w", err)
	}
	commit, err := git(dest, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if err := RunBuild(ctx, dest, BuildEnv(dest, repo.Name, commit), logPath, r.Cfg.BuildTimeout); err != nil {
		return fmt.Errorf("%w\n--- last lines of %s ---\n%s", err, logPath, tail(logPath, 30))
	}
	res, err := Prepare(dest, commit)
	if err != nil {
		return err
	}
	return r.publish(ctx, repo.Name, res)
}

func (r *Runner) publish(ctx context.Context, name string, res *Result) error {
	log.Printf("[%s] publishing %s %s with %d artifacts", name, res.Manifest.App.ID, res.Manifest.App.Version, len(res.Manifest.App.Artifacts))
	body, err := Publish(ctx, r.Cfg.Server, r.ID, res)
	if err != nil {
		return err
	}
	log.Printf("[%s] published: %s", name, body)
	return nil
}

func (r *Runner) Once(ctx context.Context, force bool, only map[string]bool) int {
	failures := 0
	for _, repo := range r.Cfg.Repos {
		if len(only) > 0 && !only[repo.Name] {
			continue
		}
		if err := r.Process(ctx, repo, force); err != nil {
			failures++
			log.Printf("[%s] failed: %v", repo.Name, err)
		}
		if ctx.Err() != nil {
			break
		}
	}
	return failures
}

func (r *Runner) Loop(ctx context.Context) {
	for {
		r.Once(ctx, false, nil)
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.Cfg.Interval):
		}
	}
}
