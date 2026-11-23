package proto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const (
	HeaderKey  = "X-Nekpo-Key"
	HeaderTime = "X-Nekpo-Time"
	HeaderSig  = "X-Nekpo-Signature"

	MaxManifest = 8 << 20
	MaxSkew     = 600
)

type Field struct {
	Key   string `json:"k"`
	Value string `json:"v"`
}

type File struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Artifact struct {
	File
	Kind     string  `json:"kind"`
	Platform string  `json:"platform"`
	Class    string  `json:"class"`
	Arch     string  `json:"arch,omitempty"`
	Label    string  `json:"label"`
	Slug     string  `json:"slug"`
	Control  []Field `json:"control,omitempty"`
}

type App struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Tagline     string     `json:"tagline"`
	Summary     string     `json:"summary"`
	Description string     `json:"description"`
	Author      string     `json:"author"`
	License     string     `json:"license"`
	Website     string     `json:"website"`
	Source      string     `json:"source"`
	Category    string     `json:"category"`
	Accent      string     `json:"accent"`
	Tags        []string   `json:"tags"`
	Version     string     `json:"version"`
	Commit      string     `json:"commit"`
	Built       int64      `json:"built"`
	Changelog   string     `json:"changelog"`
	Icon        *File      `json:"icon,omitempty"`
	Banner      *File      `json:"banner,omitempty"`
	Screenshots []File     `json:"screenshots"`
	Artifacts   []Artifact `json:"artifacts"`
}

type Manifest struct {
	Schema  int    `json:"schema"`
	Builder string `json:"builder"`
	Issued  int64  `json:"issued"`
	App     App    `json:"app"`
}

var (
	IDRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	NameRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~-]{0,127}$`)
	VersionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+~_-]{0,63}$`)
	SlugRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	KeyIDRe   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,40}$`)
	fieldRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
	pkgRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+$`)
	archRe    = regexp.MustCompile(`^[a-z0-9-]+$`)
	shaRe     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	imageExt  = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true, ".svg": true}

	Kinds    = map[string]bool{"deb": true, "appimage": true, "tarball": true, "zip": true, "exe": true, "msi": true, "dmg": true, "apk": true, "ipa": true, "vpk": true, "nro": true, "cia": true, "other": true}
	Classes  = map[string]bool{"desktop": true, "mobile": true, "console": true, "other": true}
	Accents  = map[string]bool{"neko": true, "sakura": true, "mint": true, "ocean": true}
	reserved = map[string]bool{"filename": true, "size": true, "md5sum": true, "sha1": true, "sha256": true, "sha512": true}
)

func IsImageName(name string) bool {
	i := strings.LastIndex(name, ".")
	return i >= 0 && imageExt[strings.ToLower(name[i:])]
}

func validFile(f File, what string) error {
	if !NameRe.MatchString(f.Name) {
		return fmt.Errorf("%s: invalid file name %q", what, f.Name)
	}
	if f.Size < 0 || !shaRe.MatchString(f.SHA256) {
		return fmt.Errorf("%s %s: invalid size or sha256", what, f.Name)
	}
	return nil
}

func validURL(s string) bool {
	return s == "" || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

func (m *Manifest) Validate() error {
	a := &m.App
	if m.Schema != 1 {
		return errors.New("unsupported manifest schema")
	}
	if !IDRe.MatchString(a.ID) {
		return fmt.Errorf("invalid app id %q", a.ID)
	}
	if !VersionRe.MatchString(a.Version) {
		return fmt.Errorf("invalid version %q", a.Version)
	}
	if a.Name == "" || len(a.Name) > 80 || len(a.Tagline) > 160 || len(a.Summary) > 500 || len(a.Description) > 40000 || len(a.Changelog) > 1<<20 {
		return errors.New("name, tagline, summary, description or changelog has an invalid length")
	}
	if !validURL(a.Website) || !validURL(a.Source) {
		return errors.New("website and source must be http(s) URLs")
	}
	if a.Accent == "" {
		a.Accent = "neko"
	}
	if !Accents[a.Accent] {
		return fmt.Errorf("unknown accent %q", a.Accent)
	}
	if len(a.Tags) > 12 || len(a.Screenshots) > 12 || len(a.Artifacts) == 0 || len(a.Artifacts) > 64 {
		return errors.New("too many or too few tags, screenshots or artifacts")
	}
	seen := map[string]bool{}
	for _, f := range append(append([]File{}, a.Screenshots...), optFiles(a.Icon, a.Banner)...) {
		if err := validFile(f, "asset"); err != nil {
			return err
		}
		if !IsImageName(f.Name) || seen["a:"+f.Name] {
			return fmt.Errorf("asset %s: not an image or duplicated", f.Name)
		}
		seen["a:"+f.Name] = true
	}
	slugs := map[string]bool{}
	for _, art := range a.Artifacts {
		if err := validFile(art.File, "artifact"); err != nil {
			return err
		}
		if seen["f:"+art.Name] || slugs[art.Slug] {
			return fmt.Errorf("artifact %s: duplicate file name or slug", art.Name)
		}
		seen["f:"+art.Name] = true
		slugs[art.Slug] = true
		if !Kinds[art.Kind] || !Classes[art.Class] || !SlugRe.MatchString(art.Slug) || len(art.Label) > 80 || len(art.Platform) > 32 || len(art.Arch) > 32 {
			return fmt.Errorf("artifact %s: invalid kind, class, slug, label, platform or arch", art.Name)
		}
		if art.Kind == "deb" {
			if err := validControl(art); err != nil {
				return err
			}
		}
	}
	return nil
}

func optFiles(fs ...*File) []File {
	var out []File
	for _, f := range fs {
		if f != nil {
			out = append(out, *f)
		}
	}
	return out
}

func ControlGet(c []Field, key string) string {
	for _, f := range c {
		if strings.EqualFold(f.Key, key) {
			return f.Value
		}
	}
	return ""
}

func validControl(art Artifact) error {
	if !strings.HasSuffix(art.Name, ".deb") {
		return fmt.Errorf("artifact %s: deb artifacts must end in .deb", art.Name)
	}
	if !pkgRe.MatchString(ControlGet(art.Control, "Package")) || !archRe.MatchString(ControlGet(art.Control, "Architecture")) || ControlGet(art.Control, "Version") == "" {
		return fmt.Errorf("artifact %s: control needs a valid Package, Version and Architecture", art.Name)
	}
	for _, f := range art.Control {
		if !fieldRe.MatchString(f.Key) || reserved[strings.ToLower(f.Key)] {
			return fmt.Errorf("artifact %s: control field %q is not allowed", art.Name, f.Key)
		}
		if strings.Contains(f.Value, "\n\n") || strings.HasPrefix(f.Value, "\n") || strings.HasSuffix(f.Value, "\n") {
			return fmt.Errorf("artifact %s: control field %s has a blank line", art.Name, f.Key)
		}
		for i, line := range strings.Split(f.Value, "\n") {
			if i > 0 && !strings.HasPrefix(line, " ") {
				return fmt.Errorf("artifact %s: control field %s has a bad continuation line", art.Name, f.Key)
			}
		}
	}
	return nil
}

func SumHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func Canonical(method, path string, ts int64, bodySum string) []byte {
	return []byte(fmt.Sprintf("NEKPO1\n%s\n%s\n%d\n%s", method, path, ts, bodySum))
}

func Sign(priv ed25519.PrivateKey, method, path string, ts int64, bodySum string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, Canonical(method, path, ts, bodySum)))
}

func Verify(pub ed25519.PublicKey, sig, method, path string, ts int64, bodySum string) bool {
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, Canonical(method, path, ts, bodySum), raw)
}

type Identity struct {
	ID  string
	Key ed25519.PrivateKey
}

func GenerateIdentity(id string) (Identity, error) {
	if !KeyIDRe.MatchString(id) {
		return Identity{}, errors.New("key id may only contain letters, digits, dot, dash and underscore")
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	return Identity{ID: id, Key: priv}, err
}

func (i Identity) Marshal() string {
	return fmt.Sprintf("nekpo-ed25519-v1 %s %s\n", i.ID, base64.StdEncoding.EncodeToString(i.Key.Seed()))
}

func (i Identity) PublicLine() string {
	pub := i.Key.Public().(ed25519.PublicKey)
	return fmt.Sprintf("%s %s", i.ID, base64.StdEncoding.EncodeToString(pub))
}

func (i Identity) Save(path string) error {
	return os.WriteFile(path, []byte(i.Marshal()), 0o600)
}

func LoadIdentity(path string) (Identity, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Identity{}, err
	}
	parts := strings.Fields(string(raw))
	if len(parts) != 3 || parts[0] != "nekpo-ed25519-v1" || !KeyIDRe.MatchString(parts[1]) {
		return Identity{}, fmt.Errorf("%s: not a nekpo key file", path)
	}
	seed, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil || len(seed) != ed25519.SeedSize {
		return Identity{}, fmt.Errorf("%s: corrupt key", path)
	}
	return Identity{ID: parts[1], Key: ed25519.NewKeyFromSeed(seed)}, nil
}

func LoadAuthorized(path string) (map[string]ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]ed25519.PublicKey{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 || !KeyIDRe.MatchString(parts[0]) {
			return nil, fmt.Errorf("%s: bad line %q", path, line)
		}
		pub, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%s: bad public key for %s", path, parts[0])
		}
		out[parts[0]] = ed25519.PublicKey(pub)
	}
	return out, nil
}
