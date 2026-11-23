package installer

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"nekpo/internal/archive"
	"nekpo/internal/builder"
	"nekpo/internal/conf"
	"nekpo/internal/paths"
	"nekpo/internal/pkgdb"
	"nekpo/internal/proto"
	"nekpo/internal/ui"
)

type Meta struct {
	Bin     []string
	Desktop string
	Icon    string
}

type placeIn struct {
	ID      string
	Name    string
	Version string
	File    string
	Art     proto.Artifact
	Meta    Meta
}

func readInstallMeta(dir string) Meta {
	f, err := conf.Load(filepath.Join(dir, builder.NekpoDir, builder.MetaFile))
	if err != nil {
		return Meta{}
	}
	s := f.First("install")
	if s == nil {
		return Meta{}
	}
	return Meta{Bin: s.List("bin"), Desktop: s.Get("desktop"), Icon: s.Get("icon")}
}

func hostPlatform() string {
	if runtime.GOOS == "darwin" {
		return "macos"
	}
	return runtime.GOOS
}

func Pick(arts []proto.Artifact, prefer string) (proto.Artifact, error) {
	host, arch := hostPlatform(), proto.HostArch()
	rank := map[string]int{"tarball": 0, "appimage": 1}
	best, bestRank := -1, 99
	var seen []string
	for i, a := range arts {
		seen = append(seen, fmt.Sprintf("%s/%s/%s", a.Kind, a.Platform, a.Arch))
		r, ok := rank[a.Kind]
		if !ok {
			continue
		}
		if prefer != "" {
			if a.Kind != prefer {
				continue
			}
			r = 0
		}
		if !strings.EqualFold(a.Platform, host) {
			continue
		}
		if na := proto.NormalizeArch(a.Arch); na != arch && na != proto.NoArch {
			continue
		}
		if r < bestRank {
			best, bestRank = i, r
		}
	}
	if best < 0 {
		return proto.Artifact{}, fmt.Errorf("no installable artifact for %s/%s (tarball or AppImage); available: %s", host, arch, strings.Join(seen, ", "))
	}
	return arts[best], nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func isProgram(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4)
	n, _ := io.ReadFull(f, head)
	return n >= 2 && (bytes.HasPrefix(head, []byte("\x7fELF")) || bytes.HasPrefix(head, []byte("#!")))
}

func findExecutables(root string) []string {
	var out []string
	for _, sub := range []string{"", "bin"} {
		entries, err := os.ReadDir(filepath.Join(root, sub))
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || strings.HasPrefix(name, ".") || strings.Contains(name, ".so") {
				continue
			}
			p := filepath.Join(root, sub, name)
			st, err := os.Stat(p)
			if err != nil || !st.Mode().IsRegular() || st.Mode()&0o111 == 0 || !isProgram(p) {
				continue
			}
			out = append(out, filepath.Join(sub, name))
		}
	}
	sort.Strings(out)
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func linkBin(name, target string) (string, error) {
	lp := filepath.Join(paths.BinDir(), name)
	if st, err := os.Lstat(lp); err == nil {
		ours := false
		if st.Mode()&os.ModeSymlink != 0 {
			if dest, err := os.Readlink(lp); err == nil && paths.Inside(paths.VarDir(), dest) {
				ours = true
			}
		}
		if !ours {
			return "", fmt.Errorf("%s already exists and is not managed by nekpo", lp)
		}
		os.Remove(lp)
	}
	return lp, os.Symlink(target, lp)
}

func installDesktop(id, dir string, m Meta, links map[string]string) []string {
	if m.Desktop == "" {
		return nil
	}
	src := filepath.Join(dir, filepath.FromSlash(m.Desktop))
	if !paths.Inside(dir, src) {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		ui.Warn("desktop file %s not found in the package", m.Desktop)
		return nil
	}
	var files []string
	iconDst := ""
	if m.Icon != "" {
		isrc := filepath.Join(dir, filepath.FromSlash(m.Icon))
		if paths.Inside(dir, isrc) {
			if err := paths.Ensure(paths.IconsDir()); err == nil {
				iconDst = filepath.Join(paths.IconsDir(), id+filepath.Ext(isrc))
				if err := copyFile(isrc, iconDst, 0o644); err != nil {
					iconDst = ""
				} else {
					files = append(files, iconDst)
				}
			}
		}
	}
	lines := strings.Split(string(data), "\n")
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "Exec=") || strings.HasPrefix(l, "TryExec="):
			key, rest, _ := strings.Cut(l, "=")
			cmd, args, hasArgs := strings.Cut(rest, " ")
			if lp, ok := links[filepath.Base(cmd)]; ok {
				cmd = lp
			}
			if hasArgs {
				cmd += " " + args
			}
			lines[i] = key + "=" + cmd
		case iconDst != "" && strings.HasPrefix(l, "Icon="):
			lines[i] = "Icon=" + iconDst
		}
	}
	if err := paths.Ensure(paths.AppsDir()); err != nil {
		return files
	}
	dst := filepath.Join(paths.AppsDir(), id+".desktop")
	if err := os.WriteFile(dst, []byte(strings.Join(lines, "\n")), 0o644); err == nil {
		files = append(files, dst)
	}
	return files
}

func place(in placeIn) (*pkgdb.Package, error) {
	if err := paths.Ensure(paths.VarDir(), paths.BinDir()); err != nil {
		return nil, err
	}
	dir := pkgdb.DirName(in.Name, in.ID, in.Version)
	stage := dir + ".new"
	backup := dir + ".old"
	os.RemoveAll(stage)
	os.RemoveAll(backup)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return nil, err
	}
	var bins [][2]string
	switch in.Art.Kind {
	case "tarball":
		if archive.Kind(in.File) == "" {
			os.RemoveAll(stage)
			return nil, fmt.Errorf("cannot unpack %s: only .tar.gz, .tgz, .tar.bz2, .tar and .zip are supported", filepath.Base(in.File))
		}
		p := ui.Start("unpack " + in.Art.Name)
		err := archive.Extract(in.File, stage, true)
		p.Finish(err)
		if err != nil {
			os.RemoveAll(stage)
			return nil, err
		}
		rels := in.Meta.Bin
		if len(rels) == 0 {
			rels = findExecutables(stage)
		}
		for _, rel := range rels {
			rel = filepath.FromSlash(rel)
			if !paths.Inside(stage, filepath.Join(stage, rel)) {
				continue
			}
			bins = append(bins, [2]string{filepath.Base(rel), rel})
		}
	case "appimage":
		name := filepath.Base(in.File)
		if err := copyFile(in.File, filepath.Join(stage, name), 0o755); err != nil {
			os.RemoveAll(stage)
			return nil, err
		}
		bins = append(bins, [2]string{in.ID, name})
	default:
		os.RemoveAll(stage)
		return nil, fmt.Errorf("artifacts of kind %q cannot be installed by nekpo", in.Art.Kind)
	}
	hadOld := false
	if _, err := os.Stat(dir); err == nil {
		hadOld = true
		if err := os.Rename(dir, backup); err != nil {
			os.RemoveAll(stage)
			return nil, err
		}
	}
	if err := os.Rename(stage, dir); err != nil {
		if hadOld {
			os.Rename(backup, dir)
		}
		os.RemoveAll(stage)
		return nil, err
	}
	pkg := &pkgdb.Package{ID: in.ID, Name: in.Name, Version: in.Version, Artifact: in.Art.Name, Kind: in.Art.Kind, Dir: dir}
	linkMap := map[string]string{}
	for _, b := range bins {
		target := filepath.Join(dir, b[1])
		if _, err := os.Stat(target); err != nil {
			ui.Warn("%s is not in the package; skipped", b[1])
			continue
		}
		if st, _ := os.Stat(target); st != nil && st.Mode()&0o111 == 0 {
			os.Chmod(target, st.Mode()|0o755)
		}
		lp, err := linkBin(b[0], target)
		if err != nil {
			ui.Warn("%v", err)
			continue
		}
		pkg.Links = append(pkg.Links, lp)
		linkMap[b[0]] = lp
	}
	if in.Art.Kind == "tarball" {
		pkg.Files = installDesktop(in.ID, dir, in.Meta, linkMap)
	}
	if len(pkg.Links) == 0 {
		ui.Warn("nothing was linked into %s; the files are in %s", paths.BinDir(), dir)
	}
	os.RemoveAll(backup)
	return pkg, nil
}

func removeStale(old, cur *pkgdb.Package) {
	keep := map[string]bool{}
	for _, l := range cur.Links {
		keep[l] = true
	}
	for _, f := range cur.Files {
		keep[f] = true
	}
	for _, l := range old.Links {
		if keep[l] {
			continue
		}
		if dest, err := os.Readlink(l); err == nil && paths.Inside(paths.VarDir(), dest) {
			os.Remove(l)
		}
	}
	for _, f := range old.Files {
		if !keep[f] && (paths.Inside(paths.AppsDir(), f) || paths.Inside(paths.IconsDir(), f)) {
			os.Remove(f)
		}
	}
	if old.Dir != cur.Dir && paths.Inside(paths.VarDir(), old.Dir) && filepath.Clean(old.Dir) != filepath.Clean(paths.VarDir()) {
		os.RemoveAll(old.Dir)
	}
}

func Uninstall(db *pkgdb.DB, id string) error {
	p := db.Get(id)
	if p == nil {
		return fmt.Errorf("%s is not installed", id)
	}
	removeStale(p, &pkgdb.Package{})
	db.Delete(id)
	return db.Save()
}
