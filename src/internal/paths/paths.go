package paths

import (
	"os"
	"path/filepath"
	"strings"
)

func Home() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return os.Getenv("HOME")
}

func Local() string      { return filepath.Join(Home(), ".local") }
func DataDir() string    { return filepath.Join(Local(), "share", "nekpo") }
func VarDir() string     { return filepath.Join(Local(), "var", "nekpo") }
func BinDir() string     { return filepath.Join(Local(), "bin") }
func LogDir() string     { return filepath.Join(DataDir(), "logs") }
func CacheDir() string   { return filepath.Join(DataDir(), "cache") }
func SrcDir() string     { return filepath.Join(DataDir(), "src") }
func DBFile() string     { return filepath.Join(DataDir(), "installed.json") }
func AppsDir() string    { return filepath.Join(Local(), "share", "applications") }
func IconsDir() string   { return filepath.Join(Local(), "share", "icons") }
func ConfigDir() string  { return filepath.Join(Home(), ".config", "nekpo") }
func ClientConf() string { return filepath.Join(ConfigDir(), "nekpo.conf") }

func Ensure(dirs ...string) error {
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return filepath.Join(Home(), p[1:])
	}
	return p
}

func Inside(root, p string) bool {
	root = filepath.Clean(root)
	p = filepath.Clean(p)
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}
