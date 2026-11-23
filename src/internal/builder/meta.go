package builder

import (
	"fmt"
	"strings"

	"nekpo/internal/conf"
	"nekpo/internal/proto"
)

type Rule struct {
	Pattern  string
	Kind     string
	Platform string
	Class    string
	Arch     string
	Label    string
	Slug     string
	Optional bool
}

type Meta struct {
	App           proto.App
	VersionSpec   string
	IconPath      string
	BannerPath    string
	ShotPaths     []string
	ChangelogPath string
	Rules         []Rule
}

func LoadMeta(path string) (*Meta, error) {
	f, err := conf.Load(path)
	if err != nil {
		return nil, err
	}
	r := f.Root
	m := &Meta{
		VersionSpec:   strings.TrimSpace(r.Get("version")),
		IconPath:      r.Get("icon"),
		BannerPath:    r.Get("banner"),
		ShotPaths:     r.List("screenshots"),
		ChangelogPath: r.Get("changelog"),
	}
	m.App = proto.App{
		ID:          r.Get("id"),
		Name:        r.Get("name"),
		Tagline:     r.Get("tagline"),
		Summary:     r.Get("summary"),
		Description: r.Get("description"),
		Author:      r.Get("author"),
		License:     r.Get("license"),
		Website:     r.Get("website"),
		Source:      r.Get("source"),
		Category:    r.Get("category"),
		Accent:      strings.ToLower(r.Get("accent")),
		Tags:        r.List("tags"),
	}
	if m.App.ID == "" || m.App.Name == "" {
		return nil, fmt.Errorf("%s: id and name are required", path)
	}
	for _, s := range f.Named("artifact") {
		rule := Rule{
			Pattern:  s.Get("file"),
			Kind:     strings.ToLower(s.Get("kind")),
			Platform: strings.ToLower(s.Get("platform")),
			Class:    strings.ToLower(s.Get("class")),
			Arch:     s.Get("arch"),
			Label:    s.Get("label"),
			Slug:     s.Get("slug"),
			Optional: s.Bool("optional", false),
		}
		if rule.Pattern == "" {
			return nil, fmt.Errorf("%s: every [artifact] needs a file pattern", path)
		}
		m.Rules = append(m.Rules, rule)
	}
	if len(m.Rules) == 0 {
		return nil, fmt.Errorf("%s: at least one [artifact] section is required", path)
	}
	return m, nil
}
