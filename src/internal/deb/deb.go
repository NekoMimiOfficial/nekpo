package deb

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"nekpo/internal/proto"
)

var ErrUnsupported = errors.New("unsupported control archive compression")

func ParseControl(text string) []proto.Field {
	var out []proto.Field
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		if line == "" {
			if len(out) > 0 {
				break
			}
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if len(out) > 0 {
				out[len(out)-1].Value += "\n " + strings.TrimLeft(line, " \t")
			}
			continue
		}
		i := strings.Index(line, ":")
		if i < 1 {
			continue
		}
		out = append(out, proto.Field{Key: line[:i], Value: strings.TrimSpace(line[i+1:])})
	}
	return out
}

func ReadControlGo(file string) ([]proto.Field, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	magic := make([]byte, 8)
	if _, err := io.ReadFull(f, magic); err != nil || string(magic) != "!<arch>\n" {
		return nil, fmt.Errorf("%s: not a deb archive", file)
	}
	for {
		hdr := make([]byte, 60)
		if _, err := io.ReadFull(f, hdr); err != nil {
			return nil, fmt.Errorf("%s: no control archive found", file)
		}
		name := strings.TrimSuffix(strings.TrimSpace(string(hdr[0:16])), "/")
		size, err := strconv.ParseInt(strings.TrimSpace(string(hdr[48:58])), 10, 64)
		if err != nil || size < 0 {
			return nil, fmt.Errorf("%s: corrupt archive header", file)
		}
		if strings.HasPrefix(name, "control.tar") {
			var r io.Reader = io.LimitReader(f, size)
			switch strings.TrimPrefix(name, "control.tar") {
			case "":
			case ".gz":
				gz, err := gzip.NewReader(r)
				if err != nil {
					return nil, err
				}
				defer gz.Close()
				r = gz
			default:
				return nil, ErrUnsupported
			}
			tr := tar.NewReader(r)
			for {
				h, err := tr.Next()
				if err != nil {
					return nil, fmt.Errorf("%s: control file missing", file)
				}
				if path.Clean(h.Name) == "control" {
					data, err := io.ReadAll(io.LimitReader(tr, 1<<20))
					if err != nil {
						return nil, err
					}
					return ParseControl(string(data)), nil
				}
			}
		}
		if _, err := f.Seek(size+size%2, io.SeekCurrent); err != nil {
			return nil, err
		}
	}
}

func ReadControl(file string) ([]proto.Field, error) {
	fields, err := ReadControlGo(file)
	if err == nil || !errors.Is(err, ErrUnsupported) {
		return fields, err
	}
	out, err := exec.Command("dpkg-deb", "-f", file).Output()
	if err != nil {
		return nil, fmt.Errorf("dpkg-deb -f %s: %w", file, err)
	}
	return ParseControl(string(out)), nil
}
