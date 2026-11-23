package proto

import (
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"
)

type DepInfo struct {
	Lang    string `json:"lang"`
	Arch    string `json:"arch"`
	Name    string `json:"name"`
	Version string `json:"version"`
	File    string `json:"file"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Pushed  int64  `json:"pushed"`
	Builder string `json:"builder"`
	Note    string `json:"note,omitempty"`
	Latest  bool   `json:"latest,omitempty"`
}

type DepManifest struct {
	Schema  int    `json:"schema"`
	Builder string `json:"builder"`
	Issued  int64  `json:"issued"`
	Lang    string `json:"lang"`
	Arch    string `json:"arch"`
	Name    string `json:"name"`
	Version string `json:"version"`
	File    File   `json:"file"`
	Replace bool   `json:"replace"`
	Note    string `json:"note"`
}

var (
	DepLangRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9+_-]{0,15}$`)
	DepArchRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,23}$`)
	DepNameRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
	DepVersionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+~_-]{0,63}$`)
)

const NoArch = "noarch"

func NormalizeArch(a string) string {
	a = strings.ToLower(strings.TrimSpace(a))
	switch a {
	case "", "all", "any", "noarch":
		return NoArch
	case "x86_64", "x64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	case "i386", "i686", "x86", "386":
		return "i386"
	case "armv7l", "armhf", "arm":
		return "armhf"
	case "native":
		return HostArch()
	}
	return a
}

func HostArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	case "386":
		return "i386"
	case "arm":
		return "armhf"
	}
	return runtime.GOARCH
}

func ValidDepKey(lang, arch, name string) error {
	if !DepLangRe.MatchString(lang) {
		return fmt.Errorf("invalid language %q", lang)
	}
	if !DepArchRe.MatchString(arch) {
		return fmt.Errorf("invalid architecture %q", arch)
	}
	if !DepNameRe.MatchString(name) {
		return fmt.Errorf("invalid package name %q", name)
	}
	return nil
}

func (m *DepManifest) Validate() error {
	if m.Schema != 1 {
		return errors.New("unsupported dependency manifest schema")
	}
	if err := ValidDepKey(m.Lang, m.Arch, m.Name); err != nil {
		return err
	}
	if !DepVersionRe.MatchString(m.Version) || m.Version == "latest" {
		return fmt.Errorf("invalid version %q", m.Version)
	}
	if err := validFile(m.File, "dependency"); err != nil {
		return err
	}
	if len(m.Note) > 500 {
		return errors.New("note is longer than 500 characters")
	}
	return nil
}
