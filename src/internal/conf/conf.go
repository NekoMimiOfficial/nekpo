package conf

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type Pair struct{ Key, Value string }

type Section struct {
	Name  string
	Pairs []Pair
}

func (s *Section) Get(key string) string {
	for _, p := range s.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

func (s *Section) Has(key string) bool {
	for _, p := range s.Pairs {
		if p.Key == key {
			return true
		}
	}
	return false
}

func (s *Section) All(key string) []string {
	var out []string
	for _, p := range s.Pairs {
		if p.Key == key {
			out = append(out, p.Value)
		}
	}
	return out
}

func (s *Section) List(key string) []string {
	var out []string
	for _, v := range s.All(key) {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func (s *Section) Int(key string, def int) int {
	v := strings.TrimSpace(s.Get(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func (s *Section) Bool(key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s.Get(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

type File struct {
	Root     *Section
	Sections []*Section
}

func (f *File) Named(name string) []*Section {
	var out []*Section
	for _, s := range f.Sections {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
}

func (f *File) First(name string) *Section {
	if s := f.Named(name); len(s) > 0 {
		return s[0]
	}
	return &Section{Name: name}
}

func Parse(r io.Reader) (*File, error) {
	f := &File{Root: &Section{}}
	cur := f.Root
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			cur = &Section{Name: strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))}
			f.Sections = append(f.Sections, cur)
			continue
		}
		i := strings.Index(line, "=")
		if i < 1 {
			return nil, fmt.Errorf("line %d: expected key = value", n)
		}
		key := strings.ToLower(strings.TrimSpace(line[:i]))
		val := strings.TrimSpace(line[i+1:])
		if val == `"""` {
			var buf []string
			closed := false
			for sc.Scan() {
				n++
				t := strings.TrimRight(sc.Text(), "\r")
				if strings.TrimSpace(t) == `"""` {
					closed = true
					break
				}
				buf = append(buf, t)
			}
			if !closed {
				return nil, fmt.Errorf("line %d: unterminated block", n)
			}
			val = strings.Trim(strings.Join(buf, "\n"), "\n")
		} else if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		cur.Pairs = append(cur.Pairs, Pair{key, val})
	}
	return f, sc.Err()
}

func Load(path string) (*File, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	f, err := Parse(fh)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}
