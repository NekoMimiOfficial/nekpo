package builder

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nekpo/internal/conf"
)

type Repo struct{ Name, URL, Branch string }

type Config struct {
	Server       string
	KeyFile      string
	WorkDir      string
	Interval     time.Duration
	BuildTimeout time.Duration
	RetryAfter   time.Duration
	Repos        []Repo
}

func LoadConfig(path string) (*Config, error) {
	f, err := conf.Load(path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	abs := func(p string) string {
		if p == "~" || strings.HasPrefix(p, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(home, p[1:])
			}
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if a, err := filepath.Abs(p); err == nil {
			return a
		}
		return p
	}
	r := f.Root
	c := &Config{
		Server:       strings.TrimRight(r.Get("server"), "/"),
		KeyFile:      abs(orDefault(r.Get("key_file"), "builder.key")),
		WorkDir:      abs(orDefault(r.Get("work_dir"), "work")),
		Interval:     time.Duration(r.Int("poll_seconds", 300)) * time.Second,
		BuildTimeout: time.Duration(r.Int("build_timeout_minutes", 120)) * time.Minute,
		RetryAfter:   time.Duration(r.Int("retry_failed_minutes", 60)) * time.Minute,
	}
	u, err := url.Parse(c.Server)
	if err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("server must be a URL without a path, got %q", c.Server)
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && !local && !r.Bool("allow_insecure", false) {
		return nil, fmt.Errorf("server must use https (set allow_insecure = true to override)")
	}
	for _, s := range f.Named("repo") {
		repo := Repo{Name: s.Get("name"), URL: s.Get("url"), Branch: s.Get("branch")}
		if repo.Name == "" || repo.URL == "" {
			return nil, fmt.Errorf("every [repo] needs a name and a url")
		}
		c.Repos = append(c.Repos, repo)
	}
	return c, nil
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
