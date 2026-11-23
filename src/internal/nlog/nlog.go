package nlog

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"nekpo/internal/paths"
)

var (
	file *os.File
	path string
)

const keepLogs = 40
const maxRunLog = 8 << 20

func Open(cmd string) {
	if file != nil {
		return
	}
	if err := paths.Ensure(paths.LogDir()); err != nil {
		return
	}
	cmd = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '_'
	}, cmd)
	var name string
	if cmd == "run" {
		name = "run.log"
		if st, err := os.Stat(filepath.Join(paths.LogDir(), name)); err == nil && st.Size() > maxRunLog {
			os.Rename(filepath.Join(paths.LogDir(), name), filepath.Join(paths.LogDir(), name+".1"))
		}
	} else {
		name = fmt.Sprintf("%s-%s.log", cmd, time.Now().Format("20060102-150405"))
	}
	f, err := os.OpenFile(filepath.Join(paths.LogDir(), name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	file, path = f, f.Name()
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	prune()
}

func prune() {
	entries, err := os.ReadDir(paths.LogDir())
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".log") && e.Name() != "run.log" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keepLogs {
		os.Remove(filepath.Join(paths.LogDir(), names[0]))
		names = names[1:]
	}
}

func Path() string { return path }

func Writer() io.Writer {
	if file == nil {
		return io.Discard
	}
	return file
}

func Printf(format string, args ...any) {
	if file == nil {
		return
	}
	fmt.Fprintf(file, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}

func Close() {
	if file != nil {
		file.Close()
		file = nil
	}
}
