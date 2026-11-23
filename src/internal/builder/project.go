package builder

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"nekpo/internal/deb"
	"nekpo/internal/proto"
)

const (
	NekpoDir     = "nekpo"
	MetaFile     = "meta.conf"
	BuildScript  = "build.sh"
	ArtifactsDir = "nekpo_artifacts"
	maxAsset     = 10 << 20
)

type Upload struct {
	Field string
	Name  string
	Path  string
}

type Result struct {
	Manifest *proto.Manifest
	Uploads  []Upload
}

var (
	headerRe = regexp.MustCompile(`^(#+)\s*[vV]?([0-9][^\s]*)\s*$`)
	slugBad  = regexp.MustCompile(`[^a-z0-9._-]+`)
)

var kindLabels = map[string]string{"deb": "Debian package", "appimage": "AppImage", "tarball": "Tarball", "zip": "ZIP archive", "exe": "Windows installer", "msi": "Windows installer", "dmg": "macOS disk image", "apk": "Android APK", "ipa": "iOS IPA", "vpk": "PS Vita VPK", "nro": "Switch homebrew", "cia": "3DS CIA", "other": "Download"}

func guessKind(name string) string {
	l := strings.ToLower(name)
	for suffix, kind := range map[string]string{".deb": "deb", ".appimage": "appimage", ".tar.gz": "tarball", ".tgz": "tarball", ".tar.xz": "tarball", ".tar.zst": "tarball", ".tar.bz2": "tarball", ".zip": "zip", ".exe": "exe", ".msi": "msi", ".dmg": "dmg", ".apk": "apk", ".ipa": "ipa", ".vpk": "vpk", ".nro": "nro", ".cia": "cia"} {
		if strings.HasSuffix(l, suffix) {
			return kind
		}
	}
	return "other"
}

func guessPlatform(kind string) string {
	return map[string]string{"deb": "linux", "appimage": "linux", "tarball": "linux", "exe": "windows", "msi": "windows", "dmg": "macos", "apk": "android", "ipa": "ios", "vpk": "vita", "nro": "switch", "cia": "3ds"}[kind]
}

func classOf(platform string) string {
	switch platform {
	case "linux", "windows", "macos", "bsd":
		return "desktop"
	case "android", "ios":
		return "mobile"
	case "vita", "switch", "3ds", "ps3", "ps4", "ps5", "wii", "wiiu":
		return "console"
	}
	return "other"
}

func hashFile(p string) (int64, string, error) {
	f, err := os.Open(p)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return n, hex.EncodeToString(h.Sum(nil)), err
}

func within(root, p string) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if real != rootReal && !strings.HasPrefix(real, rootReal+string(filepath.Separator)) {
		return "", fmt.Errorf("%s resolves outside the project", p)
	}
	return real, nil
}

func Prepare(dir, commit string) (*Result, error) {
	meta, err := LoadMeta(filepath.Join(dir, NekpoDir, MetaFile))
	if err != nil {
		return nil, err
	}
	app := meta.App
	app.Commit = commit
	app.Built = time.Now().Unix()
	app.Version = resolveVersion(dir, meta)
	if app.Version == "" {
		return nil, fmt.Errorf("could not work out a version: set version in meta.conf, add a changelog, or tag the repo")
	}
	res := &Result{}

	artDir := filepath.Join(dir, NekpoDir, ArtifactsDir)
	entries, err := os.ReadDir(artDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", artDir, err)
	}
	used := map[string]bool{}
	slugs := map[string]bool{}
	for _, rule := range meta.Rules {
		matched := 0
		for _, e := range entries {
			ok, err := path.Match(rule.Pattern, e.Name())
			if err != nil {
				return nil, fmt.Errorf("bad pattern %q: %w", rule.Pattern, err)
			}
			if !ok || !e.Type().IsRegular() {
				continue
			}
			if used[e.Name()] {
				return nil, fmt.Errorf("%s matches more than one [artifact] section", e.Name())
			}
			used[e.Name()] = true
			matched++
			art, err := buildArtifact(filepath.Join(artDir, e.Name()), e.Name(), rule, slugs)
			if err != nil {
				return nil, err
			}
			app.Artifacts = append(app.Artifacts, art)
			res.Uploads = append(res.Uploads, Upload{"artifact", art.Name, filepath.Join(artDir, e.Name())})
		}
		if matched == 0 && !rule.Optional {
			return nil, fmt.Errorf("no file in %s matches %q", ArtifactsDir, rule.Pattern)
		}
	}
	for _, e := range entries {
		if !used[e.Name()] {
			log.Printf("note: %s is not matched by any [artifact] section and will not be published", e.Name())
		}
	}

	addAsset := func(rel, name string) (*proto.File, error) {
		if rel == "" {
			return nil, nil
		}
		src, err := within(dir, filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("asset %s: %w", rel, err)
		}
		name += strings.ToLower(filepath.Ext(rel))
		if !proto.IsImageName(name) {
			return nil, fmt.Errorf("asset %s is not a supported image type", rel)
		}
		size, sum, err := hashFile(src)
		if err != nil {
			return nil, err
		}
		if size > maxAsset {
			return nil, fmt.Errorf("asset %s is larger than %d MB", rel, maxAsset>>20)
		}
		res.Uploads = append(res.Uploads, Upload{"asset", name, src})
		return &proto.File{Name: name, Size: size, SHA256: sum}, nil
	}
	if app.Icon, err = addAsset(meta.IconPath, "icon"); err != nil {
		return nil, err
	}
	if app.Banner, err = addAsset(meta.BannerPath, "banner"); err != nil {
		return nil, err
	}
	for i, rel := range meta.ShotPaths {
		f, err := addAsset(rel, fmt.Sprintf("shot-%d", i+1))
		if err != nil {
			return nil, err
		}
		app.Screenshots = append(app.Screenshots, *f)
	}
	if meta.ChangelogPath != "" {
		app.Changelog, err = readChangelog(dir, meta.ChangelogPath)
		if err != nil {
			return nil, err
		}
	}
	m := &proto.Manifest{Schema: 1, App: app}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	res.Manifest = m
	return res, nil
}

func buildArtifact(file, name string, rule Rule, slugs map[string]bool) (proto.Artifact, error) {
	size, sum, err := hashFile(file)
	if err != nil {
		return proto.Artifact{}, err
	}
	art := proto.Artifact{File: proto.File{Name: name, Size: size, SHA256: sum}, Kind: rule.Kind, Platform: rule.Platform, Class: rule.Class, Arch: rule.Arch, Label: rule.Label, Slug: rule.Slug}
	if art.Kind == "" {
		art.Kind = guessKind(name)
	}
	if art.Platform == "" {
		art.Platform = guessPlatform(art.Kind)
	}
	if art.Class == "" {
		art.Class = classOf(art.Platform)
	}
	if art.Kind == "deb" {
		if art.Control, err = deb.ReadControl(file); err != nil {
			return art, err
		}
		if art.Arch == "" {
			art.Arch = proto.ControlGet(art.Control, "Architecture")
		}
	}
	if art.Label == "" {
		art.Label = kindLabels[art.Kind]
		if art.Label == "" {
			art.Label = "Download"
		}
		if art.Arch != "" {
			art.Label += " (" + art.Arch + ")"
		}
	}
	if art.Slug == "" {
		var parts []string
		for _, p := range []string{art.Platform, art.Arch, art.Kind} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		art.Slug = slugBad.ReplaceAllString(strings.ToLower(strings.Join(parts, "-")), "-")
	}
	base := art.Slug
	for i := 2; slugs[art.Slug]; i++ {
		art.Slug = fmt.Sprintf("%s-%d", base, i)
	}
	slugs[art.Slug] = true
	return art, nil
}

func resolveVersion(dir string, meta *Meta) string {
	if v := meta.VersionSpec; v != "" && !strings.EqualFold(v, "auto") {
		return v
	}
	if meta.ChangelogPath != "" {
		if raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(meta.ChangelogPath))); err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				if m := headerRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
					return m[2]
				}
			}
		}
	}
	if out, err := git(dir, "describe", "--tags", "--always"); err == nil {
		return strings.TrimPrefix(out, "v")
	}
	return ""
}

var dateRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

func readChangelog(dir, rel string) (string, error) {
	p, err := within(dir, filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("changelog: %w", err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("changelog: %w", err)
	}
	if len(raw) > 1<<20 {
		return "", fmt.Errorf("changelog is larger than 1 MB")
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r", ""), "\n")
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if m := headerRe.FindStringSubmatch(t); m != nil && !dateRe.MatchString(t) {
			if d := tagDate(dir, m[2]); d != "" {
				lines[i] = fmt.Sprintf("%s %s (%s)", m[1], strings.TrimLeft(t, "# "), d)
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}
