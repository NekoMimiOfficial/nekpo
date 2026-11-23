package tmpl

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

type Out struct {
	Path    string
	Existed bool
	Written bool
}

type file struct {
	rel  string
	body []byte
	mode os.FileMode
}

func render(name string, s *Spec) ([]byte, error) {
	raw, err := assets.ReadFile("files/" + name)
	if err != nil {
		return nil, err
	}
	t, err := template.New(name).Parse(string(raw))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, s); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return buf.Bytes(), nil
}

func static(name string, s *Spec) ([]byte, error) {
	raw, err := assets.ReadFile("files/" + name)
	if err != nil {
		return nil, err
	}
	return bytes.ReplaceAll(raw, []byte("@ID@"), []byte(s.ID)), nil
}

func (s *Spec) metaConf() []byte {
	var b strings.Builder
	w := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			fmt.Fprintf(&b, "%s = %s\n", k, strings.TrimSpace(v))
		}
	}
	w("id", s.ID)
	w("name", s.Name)
	w("tagline", oneLineKeep(s.Tagline))
	w("summary", oneLineKeep(s.Summary))
	if d := strings.TrimSpace(s.Description); d != "" {
		if strings.Contains(d, "\n") {
			fmt.Fprintf(&b, "description = \"\"\"\n%s\n\"\"\"\n", d)
		} else {
			w("description", d)
		}
	}
	if len(s.Shots) > 0 {
		fmt.Fprintf(&b, "screenshots = \"%s\"\n", strings.Join(s.Shots, ", "))
	}
	w("author", s.Author)
	w("license", s.License)
	w("website", s.Website)
	w("source", s.Source)
	w("category", s.Category)
	if len(s.Tags) > 0 {
		w("tags", strings.Join(s.Tags, ", "))
	}
	w("accent", s.Accent)
	w("icon", s.Icon)
	w("banner", s.Banner)
	if s.Changelog {
		w("changelog", "changelog.md")
	}
	v := s.Version
	if v == "" {
		v = "auto"
	}
	w("version", v)
	for _, a := range s.Artifacts {
		b.WriteString("\n[artifact]\n")
		w("file", a.Pattern)
		w("kind", a.Kind)
		w("platform", a.Platform)
		w("arch", a.Arch)
		w("label", a.Label)
		if a.Optional {
			b.WriteString("optional = true\n")
		}
	}
	if len(s.InstallBins) > 0 {
		b.WriteString("\n[install]\n")
		w("bin", strings.Join(s.InstallBins, ", "))
	}
	return []byte(b.String())
}

func oneLineKeep(v string) string {
	return strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(v))
}

func (s *Spec) depsConf() []byte {
	var b strings.Builder
	if s.DepsServer != "" {
		fmt.Fprintf(&b, "server = %s\n\n", s.DepsServer)
	}
	var order []string
	by := map[string][]DepLine{}
	for _, d := range s.Deps {
		if _, ok := by[d.Lang]; !ok {
			order = append(order, d.Lang)
		}
		by[d.Lang] = append(by[d.Lang], d)
	}
	for i, lang := range order {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[%s]\n", lang)
		for _, d := range by[lang] {
			if d.Arch != "" && d.Arch != "noarch" {
				fmt.Fprintf(&b, "%s %s\n", d.Spec, d.Arch)
			} else {
				fmt.Fprintf(&b, "%s\n", d.Spec)
			}
		}
	}
	return []byte(b.String())
}

const mitText = ` Permission is hereby granted, free of charge, to any person obtaining a copy
 of this software and associated documentation files (the "Software"), to deal
 in the Software without restriction, including without limitation the rights
 to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 copies of the Software, and to permit persons to whom the Software is
 furnished to do so, subject to the following conditions:
 .
 The above copyright notice and this permission notice shall be included in
 all copies or substantial portions of the Software.
 .
 THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 SOFTWARE.`

var commonLicenses = map[string]string{"Apache-2.0": "Apache-2.0", "GPL-3.0": "GPL-3", "GPL-2.0": "GPL-2", "LGPL-3.0": "LGPL-3", "MPL-2.0": "MPL-2.0"}

func (s *Spec) copyright() []byte {
	var b strings.Builder
	b.WriteString("Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/\n")
	fmt.Fprintf(&b, "Upstream-Name: %s\n", s.Name)
	if s.Author != "" {
		fmt.Fprintf(&b, "Upstream-Contact: %s\n", oneLine(s.Author))
	}
	if s.Source != "" {
		fmt.Fprintf(&b, "Source: %s\n", s.Source)
	}
	lic := s.License
	if lic == "" {
		lic = "custom"
	}
	fmt.Fprintf(&b, "\nFiles: *\nCopyright: %d %s\nLicense: %s\n\nLicense: %s\n", time.Now().Year(), s.Maintainer(), lic, lic)
	switch {
	case lic == "MIT":
		b.WriteString(mitText + "\n")
	case commonLicenses[lic] != "":
		fmt.Fprintf(&b, " On Debian systems, the full text of the license can be found in\n /usr/share/common-licenses/%s.\n", commonLicenses[lic])
	default:
		b.WriteString(" See the LICENSE file in the source distribution.\n")
	}
	return []byte(b.String())
}

func (s *Spec) gitignoreLines() []string {
	lines := []string{"nekpo/nekpo_artifacts/", ".nekpo-deps-tmp/"}
	if s.Cmake {
		if s.HasDesktop() {
			lines = append(lines, "build-desktop/")
		}
		if s.HasVita() {
			lines = append(lines, "build-vita/")
		}
	}
	for _, d := range s.Deps {
		name, _, _ := strings.Cut(d.Spec, ":")
		if d.Lang == "cpp" {
			lines = append(lines, "third_party/"+name+"/")
		} else {
			lines = append(lines, "deps/"+d.Lang+"/"+name+"/")
		}
	}
	return lines
}

func (s *Spec) plan() ([]file, error) {
	var out []file
	add := func(rel string, body []byte, mode os.FileMode) { out = append(out, file{rel, body, mode}) }
	addT := func(rel, tpl string, mode os.FileMode) error {
		body, err := render(tpl, s)
		if err != nil {
			return err
		}
		add(rel, body, mode)
		return nil
	}
	add("nekpo/meta.conf", s.metaConf(), 0o644)
	if s.Cmake {
		if err := addT("nekpo/build.sh", "build_cmake.sh.tmpl", 0o755); err != nil {
			return nil, err
		}
		if err := addT("CMakeLists.txt", "CMakeLists.txt.tmpl", 0o644); err != nil {
			return nil, err
		}
		if err := addT("cmake/Dependencies.cmake", "cmake_Dependencies.cmake.tmpl", 0o644); err != nil {
			return nil, err
		}
		if s.HasDesktop() {
			if err := addT("cmake/platform/Desktop.cmake", "cmake_platform_Desktop.cmake.tmpl", 0o644); err != nil {
				return nil, err
			}
		}
		if s.HasVita() {
			if err := addT("cmake/platform/Vita.cmake", "cmake_platform_Vita.cmake.tmpl", 0o644); err != nil {
				return nil, err
			}
		}
		if err := addT("src/main."+s.SrcExt(), "main."+s.SrcExt()+".tmpl", 0o644); err != nil {
			return nil, err
		}
		if s.HasDesktop() && s.Packaging() {
			if err := addT("target_deps/Packaging.cmake", "target_deps_Packaging.cmake.tmpl", 0o644); err != nil {
				return nil, err
			}
			if err := addT("target_deps/package_info.cmake", "target_deps_package_info.cmake.tmpl", 0o644); err != nil {
				return nil, err
			}
			gen, err := static("target_deps_GenChangelog.cmake", s)
			if err != nil {
				return nil, err
			}
			add("target_deps/GenChangelog.cmake", gen, 0o644)
			if s.Deb || s.AppImage {
				if err := addT("target_deps/"+s.ID+".desktop", "target_deps_app.desktop.tmpl", 0o644); err != nil {
					return nil, err
				}
			}
			if s.AppImage {
				sh, err := static("target_deps_build_appimage.sh", s)
				if err != nil {
					return nil, err
				}
				add("target_deps/build_appimage.sh", sh, 0o755)
			}
			if s.Deb {
				add("target_deps/debian/copyright", s.copyright(), 0o644)
			}
		}
	} else {
		if err := addT("nekpo/build.sh", "build_generic.sh.tmpl", 0o755); err != nil {
			return nil, err
		}
	}
	if len(s.Deps) > 0 {
		add("nekpo/deps.conf", s.depsConf(), 0o644)
	}
	if s.Changelog {
		if err := addT("changelog.md", "changelog.md.tmpl", 0o644); err != nil {
			return nil, err
		}
	}
	if s.Readme {
		if err := addT("README.md", "README.md.tmpl", 0o644); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Spec) Generate() ([]Out, error) {
	if s.Dir == "" {
		return nil, errors.New("no project directory")
	}
	files, err := s.plan()
	if err != nil {
		return nil, err
	}
	var res []Out
	for _, f := range files {
		p := filepath.Join(s.Dir, filepath.FromSlash(f.rel))
		o := Out{Path: f.rel}
		if _, err := os.Stat(p); err == nil && !s.Force {
			o.Existed = true
			res = append(res, o)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return res, err
		}
		if err := os.WriteFile(p, f.body, f.mode); err != nil {
			return res, err
		}
		if err := os.Chmod(p, f.mode); err != nil {
			return res, err
		}
		o.Written = true
		res = append(res, o)
	}
	if s.Gitignore {
		o, err := s.updateGitignore()
		if err != nil {
			return res, err
		}
		res = append(res, o)
	}
	return res, nil
}

func (s *Spec) updateGitignore() (Out, error) {
	p := filepath.Join(s.Dir, ".gitignore")
	raw, _ := os.ReadFile(p)
	have := map[string]bool{}
	for _, l := range strings.Split(string(raw), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	var add []string
	for _, l := range s.gitignoreLines() {
		if !have[l] {
			add = append(add, l)
		}
	}
	o := Out{Path: ".gitignore"}
	if len(add) == 0 {
		o.Existed = true
		return o, nil
	}
	text := string(raw)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += strings.Join(add, "\n") + "\n"
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		return o, err
	}
	o.Written = true
	return o, nil
}
