package tmpl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"nekpo/internal/builder"
	"nekpo/internal/depsconf"
	"nekpo/internal/proto"
	"nekpo/internal/ui"
)

type WizardOptions struct {
	Dir      string
	Force    bool
	Server   string
	ConfPath string
}

var (
	nonSlug   = regexp.MustCompile(`[^a-z0-9]+`)
	targetRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
	titleIDRe = regexp.MustCompile(`^[A-Z0-9]{9}$`)
)

type platformDef struct {
	key   string
	label string
}

var allPlatforms = []platformDef{
	{"linux", "Linux desktop"},
	{"vita", "PS Vita"},
	{"windows", "Windows"},
	{"macos", "macOS"},
	{"android", "Android"},
	{"switch", "Nintendo Switch"},
	{"3ds", "Nintendo 3DS"},
	{"web", "Web"},
	{"other", "Other"},
}

var kinds = []string{"tarball", "zip", "deb", "appimage", "exe", "msi", "dmg", "apk", "vpk", "nro", "cia", "other"}

var kindByPlatform = map[string]string{"linux": "tarball", "windows": "exe", "macos": "dmg", "android": "apk", "vita": "vpk", "switch": "nro", "3ds": "cia"}

func slug(v string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(v), "-"), "-")
}

func titleName(id string) string {
	parts := strings.Split(id, "-")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func exists(dir, rel string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
	return err == nil
}

func askValid(q, def string, ok func(string) bool, hint string) (string, error) {
	for i := 0; i < 5; i++ {
		v := ui.Ask(q, def)
		if ok(v) {
			return v, nil
		}
		ui.Warn("%s", hint)
	}
	return "", errors.New("too many invalid answers")
}

func hostArchName() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	}
	return runtime.GOARCH
}

func indexOf(list []string, v string) int {
	for i, x := range list {
		if x == v {
			return i
		}
	}
	return len(list) - 1
}

func Run(o WizardOptions) error {
	s, err := ask(o)
	if err != nil {
		return err
	}
	files, err := s.plan()
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr)
	ui.Info("Files that will be created:")
	for _, f := range files {
		state := "new"
		if exists(s.Dir, f.rel) {
			state = "exists, kept"
			if s.Force {
				state = "exists, replaced"
			}
		}
		ui.Note("%s (%s)", f.rel, state)
	}
	if s.Gitignore {
		ui.Note(".gitignore (entries added)")
	}
	if !ui.Confirm("Create them?", true) {
		return errors.New("cancelled, nothing was written")
	}
	res, err := s.Generate()
	if err != nil {
		return err
	}
	for _, r := range res {
		switch {
		case r.Written:
			ui.OK("wrote %s", r.Path)
		case r.Existed:
			ui.Note("kept existing %s", r.Path)
		}
	}
	offerRepo(s, o)
	s.PrintChecklist()
	return nil
}

func offerRepo(s *Spec, o WizardOptions) {
	if s.LocalOnly {
		ui.Info("Local-only project: publish it from this machine with 'nekpo publish'.")
		return
	}
	if s.Source == "" {
		ui.Warn("No source URL set, so there is no [repo] entry to suggest for builder.conf.")
		return
	}
	block := fmt.Sprintf("\n[repo]\nname = %s\nurl = %s\n", s.Name, s.Source)
	if o.ConfPath == "" {
		ui.Info("Add this to your builder.conf so the builder picks the project up:%s", block)
		return
	}
	if ui.Confirm("Append a [repo] entry for this project to "+o.ConfPath+"?", false) {
		f, err := os.OpenFile(o.ConfPath, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			ui.Fail("%v", err)
			return
		}
		defer f.Close()
		if _, err := f.WriteString(block); err != nil {
			ui.Fail("%v", err)
			return
		}
		ui.OK("updated %s", o.ConfPath)
	}
}

func ask(o WizardOptions) (*Spec, error) {
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Spec{Dir: dir, Force: o.Force}
	ui.Info("Project setup in %s", dir)
	ui.Note("nekpo reads a nekpo/ folder in your project: meta.conf (store page and artifacts), build.sh (how it is built)")
	ui.Note("and optionally deps.conf (dependencies pulled from your server). Press Enter to skip any optional question.")
	fmt.Fprintln(os.Stderr)

	mode := ui.Choose("What kind of project is this?", []string{
		"Git project: the builder pulls it from a repository",
		"Local only: only the nekpo/ data is created, to publish from this machine with 'nekpo publish'",
	}, 0)
	s.LocalOnly = mode == 1

	fmt.Fprintln(os.Stderr)
	ui.Info("Store metadata (nekpo/meta.conf)")
	id, err := askValid("App id (lowercase letters, digits and dashes)", slug(filepath.Base(dir)), func(v string) bool { return proto.IDRe.MatchString(v) }, "use lowercase letters, digits and dashes, starting with a letter or digit")
	if err != nil {
		return nil, err
	}
	s.ID = id
	name, err := askValid("Display name", titleName(id), func(v string) bool { return v != "" && len(v) <= 80 }, "a name of up to 80 characters is required")
	if err != nil {
		return nil, err
	}
	s.Name = name
	s.Version = ui.Ask("Version ('auto' reads changelog.md, then git tags)", "auto")
	s.Tagline = ui.Ask("Tagline (one short line)", "")
	s.Summary = ui.Ask("Summary (one or two sentences)", "")
	s.Description = ui.Ask("Description (one line; use meta.conf for longer markdown)", "")
	s.Author = ui.Ask("Author", "")
	licenses := []string{"Apache-2.0", "MIT", "GPL-3.0", "GPL-2.0", "LGPL-3.0", "MPL-2.0", "BSD-3-Clause", "Other or none"}
	li := ui.Choose("License", licenses, 0)
	if li < len(licenses)-1 {
		s.License = licenses[li]
	} else {
		s.License = ui.Ask("License identifier", "")
	}
	s.Website = ui.Ask("Website URL", "")
	remote := ""
	if out, err := builder.Git(dir, "remote", "get-url", "origin"); err == nil {
		remote = out
	}
	s.Source = ui.Ask("Source repository URL", remote)
	s.Category = ui.Ask("Category (for example Music, Tools, Games)", "")
	s.Tags = splitList(ui.Ask("Tags (comma separated)", ""))
	accents := []string{"neko", "sakura", "mint", "ocean"}
	s.Accent = accents[ui.Choose("Accent colour of the store page", accents, 0)]
	iconDef := ""
	for _, c := range []string{"assets/logo.png", "assets/icon.png", "icon.png", "logo.png"} {
		if exists(dir, c) {
			iconDef = c
			break
		}
	}
	s.Icon = ui.Ask("Icon file, relative to the project (PNG, 512x512 recommended)", iconDef)
	s.Banner = ui.Ask("Banner image (optional)", "")
	s.Shots = findShots(dir)
	if len(s.Shots) > 0 {
		ui.Note("found %d screenshots in nekpo/screenshots", len(s.Shots))
	} else {
		s.Shots = splitList(ui.Ask("Screenshot files, comma separated (optional)", ""))
	}

	fmt.Fprintln(os.Stderr)
	ui.Info("Build setup (nekpo/build.sh)")
	langs := []string{"C++", "C", "Go", "Rust", "Python", "Other or shell"}
	langKeys := []string{"cpp", "c", "go", "rust", "python", "other"}
	s.Lang = langKeys[ui.Choose("Main language", langs, 0)]

	if s.Lang == "cpp" || s.Lang == "c" {
		if exists(dir, "CMakeLists.txt") && !o.Force {
			ui.Note("CMakeLists.txt already exists and will not be touched")
			s.BuildCmd = "cmake -S \"$root\" -B \"$root/build\" -DCMAKE_BUILD_TYPE=Release\ncmake --build \"$root/build\" -j\"$(nproc)\""
		} else {
			s.Cmake = ui.Confirm("Generate a CMake project (CMakeLists, platform layer, packaging targets)?", true)
		}
	}
	if s.Cmake {
		if err := askCmake(s); err != nil {
			return nil, err
		}
	} else {
		askGeneric(s)
	}

	fmt.Fprintln(os.Stderr)
	ui.Info("Dependencies (nekpo/deps.conf)")
	if ui.Confirm("Pull dependencies from your nekpo server before building?", false) {
		s.DepsServer = ui.Ask("Server URL for dependencies (Enter to use your nekpo config)", o.Server)
		defLang := s.Lang
		if defLang == "other" {
			defLang = "cpp"
		}
		for {
			spec := ui.Ask("Dependency as name or name:version (Enter when done)", "")
			if spec == "" {
				break
			}
			lang := ui.Ask("Language of this dependency", defLang)
			arch := ui.Ask("Architecture (noarch, amd64, arm64, armhf, ...)", "noarch")
			d, err := depsconf.ParseDep(lang, spec, arch)
			if err != nil {
				ui.Warn("%v", err)
				continue
			}
			s.Deps = append(s.Deps, DepLine{Lang: d.Lang, Spec: spec, Arch: d.Arch})
		}
	}

	fmt.Fprintln(os.Stderr)
	ui.Info("Extra files")
	if exists(dir, "changelog.md") {
		s.Changelog = true
	} else if ui.Confirm("Create a starter changelog.md?", true) {
		s.Changelog = true
	}
	if !exists(dir, "README.md") {
		s.Readme = ui.Confirm("Create a starter README.md?", true)
	}
	s.Gitignore = ui.Confirm("Add build and artifact folders to .gitignore?", true)
	return s, nil
}

func findShots(dir string) []string {
	var out []string
	entries, err := os.ReadDir(filepath.Join(dir, "nekpo", "screenshots"))
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".png", ".jpg", ".jpeg", ".webp", ".gif":
			out = append(out, "nekpo/screenshots/"+e.Name())
		}
	}
	sort.Strings(out)
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

func askCmake(s *Spec) error {
	cmakePlatforms := []string{"Linux desktop", "PS Vita"}
	keys := []string{"linux", "vita"}
	for _, i := range ui.ChooseMulti("Platforms to build", cmakePlatforms, []int{0}) {
		s.Platforms = append(s.Platforms, keys[i])
	}
	if len(s.Platforms) == 0 {
		s.Platforms = []string{"linux"}
	}
	if s.IsC() {
		std := []string{"11", "17", "99"}
		s.Std = std[ui.Choose("C standard", std, 0)]
	} else {
		std := []string{"17", "20", "14", "23", "11"}
		s.Std = std[ui.Choose("C++ standard", std, 0)]
	}
	t, err := askValid("Executable name", s.ID, func(v string) bool { return targetRe.MatchString(v) }, "use letters, digits, dashes and underscores")
	if err != nil {
		return err
	}
	s.Target = t
	s.PkgConfig = splitList(ui.Ask("pkg-config modules to use (comma separated, for example sdl2, libcurl)", ""))
	s.LinkLibs = splitList(ui.Ask("Extra libraries to link (comma separated, for example m, dl)", ""))
	s.DepTargets = splitList(ui.Ask("CMake targets provided by third_party folders to link (for example NekoUI::NekoUI)", ""))
	s.AssetsDir = strings.Trim(ui.Ask("Runtime data folder copied next to the binary (for example assets/data)", ""), "/")
	if s.Icon != "" {
		s.IconPath = s.Icon
	} else {
		s.IconPath = "assets/logo.png"
	}
	if s.HasDesktop() {
		opts := []string{"Tarball (.tar.gz)", "Debian package (.deb)", "AppImage"}
		for _, i := range ui.ChooseMulti("Linux outputs to build", opts, []int{0}) {
			switch i {
			case 0:
				s.Tarball = true
			case 1:
				s.Deb = true
			case 2:
				s.AppImage = true
			}
		}
		if !s.Packaging() {
			ui.Warn("a Linux build needs at least one output; using a tarball")
			s.Tarball = true
		}
		s.TarballArch = hostArchName()
		if s.Deb {
			s.DebDepends = ui.Ask("Debian runtime dependencies (comma separated, Enter lets dpkg work them out)", "")
			s.DebSection = ui.Ask("Debian section", "utils")
		}
		if s.Deb || s.AppImage {
			s.DesktopCategories = ui.Ask("Desktop menu categories", "Utility;")
			if !strings.HasSuffix(s.DesktopCategories, ";") {
				s.DesktopCategories += ";"
			}
		}
		if s.Tarball {
			s.Artifacts = append(s.Artifacts, Artifact{Kind: "tarball", Platform: "linux", Arch: s.TarballArch, Pattern: s.ID + "-*-linux-*.tar.gz", Label: "Linux tarball"})
			s.InstallBins = []string{s.Target}
		}
		if s.Deb {
			s.Artifacts = append(s.Artifacts, Artifact{Kind: "deb", Platform: "linux", Pattern: "*.deb"})
		}
		if s.AppImage {
			s.Artifacts = append(s.Artifacts, Artifact{Kind: "appimage", Platform: "linux", Arch: s.TarballArch, Pattern: "*.AppImage", Label: "AppImage", Optional: true})
		}
	}
	if s.HasVita() {
		def := strings.ToUpper(nonSlug.ReplaceAllString(strings.ToLower(s.ID), ""))
		for len(def) < 4 {
			def += "X"
		}
		def = def[:4] + "00001"
		v, err := askValid("Vita title ID (9 uppercase letters or digits)", def, func(v string) bool { return titleIDRe.MatchString(v) }, "a title ID is 9 uppercase letters or digits")
		if err != nil {
			return err
		}
		s.VitaTitleID = v
		s.VitaVersion = ui.Ask("Vita app version", "01.00")
		s.Artifacts = append(s.Artifacts, Artifact{Kind: "vpk", Platform: "vita", Pattern: "*.vpk", Label: "PS Vita VPK"})
	}
	return nil
}

func askGeneric(s *Spec) {
	ui.Note("No CMake generation: nekpo/build.sh runs your own build and must write the artifacts into $NEKPO_ARTIFACTS")
	var pl []string
	for _, p := range allPlatforms {
		pl = append(pl, p.label)
	}
	for _, i := range ui.ChooseMulti("Platforms this project ships for", pl, []int{0}) {
		s.Platforms = append(s.Platforms, allPlatforms[i].key)
	}
	if len(s.Platforms) == 0 {
		s.Platforms = []string{"linux"}
	}
	def := s.BuildCmd
	otherPattern := "*"
	switch s.Lang {
	case "go":
		def = fmt.Sprintf("go build -o \"$out/%s\" .", s.ID)
		otherPattern = s.ID
	case "rust":
		def = fmt.Sprintf("cargo build --release\ncp \"target/release/%s\" \"$out/\"", s.ID)
		otherPattern = s.ID
	}
	if def != "" {
		ui.Note("default build commands:\n%s", def)
		if !ui.Confirm("Use them?", true) {
			def = ""
		}
	}
	if def == "" {
		def = ui.Ask("Build command (run from the project root, writes into \"$out\"; Enter to fill in later)", "")
	}
	s.BuildCmd = def
	ui.Info("Artifacts: the files nekpo/build.sh leaves in nekpo/nekpo_artifacts that become downloads")
	for {
		if len(s.Artifacts) > 0 && !ui.Confirm("Add another artifact?", false) {
			break
		}
		s.Artifacts = append(s.Artifacts, askArtifact(s, otherPattern))
	}
	for _, a := range s.Artifacts {
		if a.Platform == "linux" && (a.Kind == "tarball" || a.Kind == "appimage") {
			s.InstallBins = splitList(ui.Ask("Programs 'nekpo install' links into ~/.local/bin (relative to the unpacked archive; Enter to detect)", ""))
			break
		}
	}
}

func askArtifact(s *Spec, otherPattern string) Artifact {
	var labels, keys []string
	for _, k := range s.Platforms {
		for _, p := range allPlatforms {
			if p.key == k {
				labels = append(labels, p.label)
				keys = append(keys, p.key)
			}
		}
	}
	plat := keys[0]
	if len(keys) > 1 {
		plat = keys[ui.Choose("Platform of this artifact", labels, 0)]
	}
	def := kindByPlatform[plat]
	if def == "" {
		def = "other"
	}
	kind := kinds[ui.Choose("Kind of artifact", kinds, indexOf(kinds, def))]
	patterns := map[string]string{"tarball": s.ID + "-*.tar.gz", "zip": "*.zip", "deb": "*.deb", "appimage": "*.AppImage", "exe": "*.exe", "msi": "*.msi", "dmg": "*.dmg", "apk": "*.apk", "vpk": "*.vpk", "nro": "*.nro", "cia": "*.cia", "other": otherPattern}
	a := Artifact{Kind: kind, Platform: plat}
	a.Pattern = ui.Ask("File name pattern in nekpo_artifacts (wildcards allowed)", patterns[kind])
	archDef := ""
	if plat == "linux" {
		archDef = hostArchName()
	}
	a.Arch = ui.Ask("Architecture (optional)", archDef)
	a.Label = ui.Ask("Label shown on the download button (optional)", "")
	a.Optional = ui.Confirm("Is this artifact optional (the build may not produce it)?", false)
	return a
}

type Item struct {
	Path    string
	Why     string
	Present bool
}

func (s *Spec) Checklist() []Item {
	var items []Item
	add := func(path, why string) {
		items = append(items, Item{path, why, exists(s.Dir, path)})
	}
	add("changelog.md", "version history; with version = auto nekpo takes the newest '# vX.Y.Z' heading as the release version")
	add("README.md", "project overview for your repository host, not required by nekpo")
	add("LICENSE", "full license text; the license id in meta.conf is shown on the store, and licenses need their text in the repository")
	if s.Icon != "" || s.Deb || s.AppImage {
		p := s.Icon
		if p == "" {
			p = s.IconPath
		}
		add(p, "app icon for the store page, the .deb and the AppImage (PNG)")
	}
	if len(s.Shots) == 0 {
		items = append(items, Item{"nekpo/screenshots/", "screenshots for the store gallery (png, jpg or webp, list them in meta.conf or drop them here and rerun init)", false})
	} else {
		for _, sh := range s.Shots {
			add(sh, "screenshot listed in meta.conf")
		}
	}
	if s.Banner != "" {
		add(s.Banner, "banner image for the store page")
	}
	if s.AssetsDir != "" {
		add(s.AssetsDir, "runtime data folder copied next to the binary and into the packages")
	}
	if s.HasVita() {
		add("assets/vita/sce_sys/icon0.png", "LiveArea icon inside the VPK; also bg0.png, startup.png and livearea/ images")
	}
	if s.Deb {
		add("target_deps/debian/copyright", "Debian copyright file (generated from your license choice, review it)")
	}
	if len(s.Deps) > 0 {
		items = append(items, Item{"nekpo/deps.lock", "created by 'nekpo build' or 'nekpo deps sync'; commit it to pin dependency checksums", exists(s.Dir, "nekpo/deps.lock")})
	}
	return items
}

func (s *Spec) PrintChecklist() {
	fmt.Fprintln(os.Stderr)
	ui.Info("Files you may still need")
	for _, it := range s.Checklist() {
		if it.Present {
			ui.OK("%s", it.Path)
		} else {
			ui.Warn("%s: %s", it.Path, it.Why)
		}
	}
	fmt.Fprintln(os.Stderr)
	ui.Info("Next: 'nekpo build' to try the build, then 'nekpo publish' or push the project to your git host.")
}
