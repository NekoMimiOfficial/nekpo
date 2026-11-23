package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"nekpo/internal/paths"
)

const maxExtract = 16 << 30

func SumFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func Kind(name string) string {
	l := strings.ToLower(name)
	switch {
	case strings.HasSuffix(l, ".tar.gz"), strings.HasSuffix(l, ".tgz"):
		return "targz"
	case strings.HasSuffix(l, ".tar.bz2"), strings.HasSuffix(l, ".tbz2"):
		return "tarbz2"
	case strings.HasSuffix(l, ".tar"):
		return "tar"
	case strings.HasSuffix(l, ".zip"):
		return "zip"
	}
	return ""
}

type entry struct {
	Name    string
	Dir     bool
	Symlink bool
	Hard    bool
	Link    string
	Mode    fs.FileMode
}

func walk(file string, fn func(e entry, r io.Reader) error) error {
	switch Kind(file) {
	case "zip":
		return walkZip(file, fn)
	case "tar", "targz", "tarbz2":
		return walkTar(file, fn)
	}
	return fmt.Errorf("unsupported archive format %q (use .tar.gz, .tgz, .tar.bz2, .tar or .zip)", filepath.Base(file))
}

func walkTar(file string, fn func(e entry, r io.Reader) error) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	switch Kind(file) {
	case "targz":
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	case "tarbz2":
		r = bzip2.NewReader(f)
	}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		e := entry{Name: h.Name, Mode: fs.FileMode(h.Mode).Perm()}
		switch h.Typeflag {
		case tar.TypeDir:
			e.Dir = true
		case tar.TypeSymlink:
			e.Symlink = true
			e.Link = h.Linkname
		case tar.TypeLink:
			e.Hard = true
			e.Link = h.Linkname
		case tar.TypeReg:
		default:
			continue
		}
		if err := fn(e, tr); err != nil {
			return err
		}
	}
}

func walkZip(file string, fn func(e entry, r io.Reader) error) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		m := zf.Mode()
		e := entry{Name: zf.Name, Mode: m.Perm()}
		if m.IsDir() || strings.HasSuffix(zf.Name, "/") {
			e.Dir = true
			if err := fn(e, nil); err != nil {
				return err
			}
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		if m&fs.ModeSymlink != 0 {
			data, _ := io.ReadAll(io.LimitReader(rc, 4096))
			rc.Close()
			e.Symlink = true
			e.Link = string(data)
			if err := fn(e, nil); err != nil {
				return err
			}
			continue
		}
		err = fn(e, rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func cleanName(name string) (string, error) {
	n := strings.ReplaceAll(name, "\\", "/")
	for _, part := range strings.Split(n, "/") {
		if part == ".." {
			return "", fmt.Errorf("unsafe path %q in archive", name)
		}
	}
	n = path.Clean(n)
	if strings.HasPrefix(n, "/") {
		return "", fmt.Errorf("absolute path %q in archive", name)
	}
	if n == "." {
		return "", nil
	}
	return n, nil
}

func commonTop(file string) (string, error) {
	tops := map[string]bool{}
	isDir := map[string]bool{}
	err := walk(file, func(e entry, r io.Reader) error {
		n, err := cleanName(e.Name)
		if err != nil {
			return err
		}
		if n == "" {
			return nil
		}
		first, rest, nested := strings.Cut(n, "/")
		tops[first] = true
		if (nested && rest != "") || e.Dir {
			isDir[first] = true
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(tops) != 1 {
		return "", nil
	}
	for t := range tops {
		if isDir[t] {
			return t, nil
		}
	}
	return "", nil
}

func Extract(file, dest string, strip bool) error {
	top := ""
	if strip {
		t, err := commonTop(file)
		if err != nil {
			return err
		}
		top = t
	}
	destAbs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destAbs, 0o755); err != nil {
		return err
	}
	var total int64
	rel := func(n string) (string, bool) {
		if top != "" {
			if n == top {
				return "", false
			}
			n = strings.TrimPrefix(n, top+"/")
		}
		return n, true
	}
	return walk(file, func(e entry, r io.Reader) error {
		name, err := cleanName(e.Name)
		if err != nil {
			return err
		}
		if name == "" {
			return nil
		}
		name, ok := rel(name)
		if !ok {
			return nil
		}
		target := filepath.Join(destAbs, filepath.FromSlash(name))
		if !paths.Inside(destAbs, target) {
			return fmt.Errorf("unsafe path %q in archive", e.Name)
		}
		switch {
		case e.Dir:
			return os.MkdirAll(target, 0o755)
		case e.Symlink:
			if filepath.IsAbs(e.Link) || strings.HasPrefix(e.Link, "/") {
				return fmt.Errorf("symlink %q points to an absolute path", e.Name)
			}
			resolved := filepath.Join(filepath.Dir(target), filepath.FromSlash(e.Link))
			if !paths.Inside(destAbs, resolved) {
				return fmt.Errorf("symlink %q points outside the archive", e.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			os.Remove(target)
			return os.Symlink(e.Link, target)
		case e.Hard:
			ln, err := cleanName(e.Link)
			if err != nil {
				return err
			}
			ln, ok := rel(ln)
			if !ok {
				return nil
			}
			src := filepath.Join(destAbs, filepath.FromSlash(ln))
			if !paths.Inside(destAbs, src) {
				return fmt.Errorf("hard link %q points outside the archive", e.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			os.Remove(target)
			return os.Link(src, target)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := e.Mode
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o200)
		if err != nil {
			return err
		}
		n, err := io.Copy(out, io.LimitReader(r, maxExtract-total+1))
		cerr := out.Close()
		if err != nil {
			return err
		}
		if cerr != nil {
			return cerr
		}
		total += n
		if total > maxExtract {
			return fmt.Errorf("archive expands to more than %d GiB", maxExtract>>30)
		}
		return os.Chmod(target, mode)
	})
}

func Create(dir, out string) error {
	root := filepath.Clean(dir)
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	gz, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		f.Close()
		return err
	}
	tw := tar.NewWriter(gz)
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relp, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if relp == "." {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		if !mode.IsRegular() && !mode.IsDir() && mode&fs.ModeSymlink == 0 {
			return nil
		}
		link := ""
		if mode&fs.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		h, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(relp)
		if mode.IsDir() {
			h.Name += "/"
		}
		h.Uid, h.Gid, h.Uname, h.Gname = 0, 0, "", ""
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if mode.IsRegular() {
			src, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, src)
			src.Close()
			return err
		}
		return nil
	})
	if err == nil {
		err = tw.Close()
	}
	if cerr := gz.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(out)
	}
	return err
}
