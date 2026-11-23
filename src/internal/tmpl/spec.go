package tmpl

import (
	"embed"
	"regexp"
	"strings"
)

//go:embed files
var assets embed.FS

type DepLine struct {
	Lang string
	Spec string
	Arch string
}

type Artifact struct {
	Kind     string
	Platform string
	Arch     string
	Pattern  string
	Label    string
	Optional bool
}

type Spec struct {
	Dir       string
	Force     bool
	LocalOnly bool

	ID          string
	Name        string
	Tagline     string
	Summary     string
	Description string
	Author      string
	License     string
	Website     string
	Source      string
	Category    string
	Accent      string
	Tags        []string
	Version     string
	Icon        string
	Banner      string
	Shots       []string

	Lang      string
	Platforms []string
	Cmake     bool
	Std       string
	Target    string
	PkgConfig []string
	LinkLibs  []string
	DepTargets []string
	AssetsDir string

	Tarball  bool
	Deb      bool
	AppImage bool
	IconPath string

	DebSection        string
	DebDepends        string
	DesktopCategories string
	TarballArch       string

	VitaTitleID string
	VitaVersion string

	BuildCmd    string
	Artifacts   []Artifact
	InstallBins []string

	Deps       []DepLine
	DepsServer string

	Changelog bool
	Readme    bool
	Gitignore bool
}

var nonWord = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func (s *Spec) has(p string) bool {
	for _, x := range s.Platforms {
		if x == p {
			return true
		}
	}
	return false
}

func (s *Spec) IsC() bool       { return s.Lang == "c" }
func (s *Spec) HasVita() bool   { return s.has("vita") }
func (s *Spec) HasDesktop() bool { return s.has("linux") }
func (s *Spec) Packaging() bool { return s.Tarball || s.Deb || s.AppImage }

func (s *Spec) SrcExt() string {
	if s.IsC() {
		return "c"
	}
	return "cpp"
}

func (s *Spec) CMakeLangs() string {
	if s.IsC() {
		return "C"
	}
	return "CXX"
}

func (s *Spec) CMakeProject() string {
	if n := nonWord.ReplaceAllString(s.Name, ""); n != "" {
		return n
	}
	return s.ID
}

func (s *Spec) Maintainer() string {
	if s.Author != "" {
		return oneLine(s.Author)
	}
	return s.ID
}

func oneLine(v string) string {
	v = strings.NewReplacer("\"", "", "\\", "", "\r", " ", "\n", " ").Replace(v)
	return strings.TrimSpace(v)
}

func (s *Spec) Summary1() string {
	for _, v := range []string{s.Summary, s.Tagline, s.Name} {
		if x := oneLine(v); x != "" {
			return x
		}
	}
	return s.ID
}

func (s *Spec) Comment() string {
	if x := oneLine(s.Tagline); x != "" {
		return x
	}
	return s.Summary1()
}

func (s *Spec) DesktopTargetList() []string {
	var out []string
	if s.Tarball {
		out = append(out, "tarball")
	}
	if s.Deb {
		out = append(out, "deb")
	}
	if s.AppImage {
		out = append(out, "appimage")
	}
	return out
}

func (s *Spec) DesktopCopies() []string {
	var out []string
	if s.Tarball {
		out = append(out, "*.tar.gz")
	}
	if s.Deb {
		out = append(out, "*.deb")
	}
	if s.AppImage {
		out = append(out, "*.AppImage")
	}
	return out
}

func (s *Spec) GhSpec() string {
	const p = "https://github.com/"
	if !strings.HasPrefix(s.Source, p) {
		return ""
	}
	r := strings.TrimSuffix(strings.TrimPrefix(s.Source, p), ".git")
	if strings.Count(r, "/") != 1 {
		return ""
	}
	return "gh+" + r
}

func (s *Spec) FirstVersion() string {
	if s.Version == "" || strings.EqualFold(s.Version, "auto") {
		return "0.1.0"
	}
	return s.Version
}
