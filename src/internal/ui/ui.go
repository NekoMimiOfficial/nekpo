package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"nekpo/internal/nlog"
)

var frames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

var (
	ttyErr  = isTTY(os.Stderr)
	ttyOut  = isTTY(os.Stdout)
	noColor = os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb"
)

func ErrColor() bool { return ttyErr && !noColor }
func OutColor() bool { return ttyOut && !noColor }

func paint(code, s string, on bool) string {
	if !on || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func Green(s string) string  { return paint("32", s, OutColor()) }
func Red(s string) string    { return paint("31", s, OutColor()) }
func Yellow(s string) string { return paint("33", s, OutColor()) }
func Cyan(s string) string   { return paint("36", s, OutColor()) }
func Dim(s string) string    { return paint("2", s, OutColor()) }
func Bold(s string) string   { return paint("1", s, OutColor()) }

func eg(s string) string { return paint("32", s, ErrColor()) }
func er(s string) string { return paint("31", s, ErrColor()) }
func ey(s string) string { return paint("33", s, ErrColor()) }
func ec(s string) string { return paint("36", s, ErrColor()) }
func ed(s string) string { return paint("2", s, ErrColor()) }
func eb(s string) string { return paint("1", s, ErrColor()) }

func Info(format string, a ...any) {
	m := fmt.Sprintf(format, a...)
	fmt.Fprintln(os.Stderr, ec("•")+" "+m)
	nlog.Printf("%s", m)
}

func OK(format string, a ...any) {
	m := fmt.Sprintf(format, a...)
	fmt.Fprintln(os.Stderr, eg("✔")+" "+m)
	nlog.Printf("ok: %s", m)
}

func Warn(format string, a ...any) {
	m := fmt.Sprintf(format, a...)
	fmt.Fprintln(os.Stderr, ey("!")+" "+m)
	nlog.Printf("warn: %s", m)
}

func Fail(format string, a ...any) {
	m := fmt.Sprintf(format, a...)
	fmt.Fprintln(os.Stderr, er("✘")+" "+m)
	nlog.Printf("error: %s", m)
}

func Note(format string, a ...any) {
	m := fmt.Sprintf(format, a...)
	fmt.Fprintln(os.Stderr, ed("  "+m))
	nlog.Printf("%s", m)
}

func Bar(pct int, on bool) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	n := pct * 20 / 100
	return "[" + paint("32", strings.Repeat("#", n), on) + paint("2", strings.Repeat("-", 20-n), on) + "] " + strconv.Itoa(pct) + "%"
}

func Elapsed(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func width() int {
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 20 {
		return n
	}
	return 80
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

type Progress struct {
	mu       sync.Mutex
	label    string
	pct      int
	note     string
	start    time.Time
	tty      bool
	color    bool
	quit     chan struct{}
	done     chan struct{}
	decade   int
	frame    int
	finished bool
}

func Start(label string) *Progress {
	p := &Progress{label: label, pct: -1, decade: -1, start: time.Now(), tty: ttyErr, color: ErrColor(), quit: make(chan struct{}), done: make(chan struct{})}
	nlog.Printf("start: %s", label)
	if p.tty {
		go p.loop()
	} else {
		fmt.Fprintf(os.Stderr, "%s ...\n", label)
		close(p.done)
	}
	return p
}

func (p *Progress) loop() {
	defer close(p.done)
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-p.quit:
			return
		case <-t.C:
			p.render()
		}
	}
}

func (p *Progress) render() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return
	}
	p.frame = (p.frame + 1) % len(frames)
	el := Elapsed(time.Since(p.start))
	plain := frames[p.frame] + " " + p.label
	line := paint("36", frames[p.frame], p.color) + " " + p.label
	if p.pct >= 0 {
		plain += " " + Bar(p.pct, false)
		line += " " + Bar(p.pct, p.color)
	}
	plain += " " + el
	line += " " + paint("2", el, p.color)
	if room := width() - 2 - utf8.RuneCountInString(plain) - 2; room > 8 && p.note != "" {
		line += "  " + paint("2", truncate(p.note, room), p.color)
	}
	fmt.Fprintf(os.Stderr, "\r\x1b[K%s", line)
}

func (p *Progress) Set(pct int) {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	p.mu.Lock()
	p.pct = pct
	show := false
	if !p.tty && pct/10 > p.decade {
		p.decade = pct / 10
		show = true
	}
	p.mu.Unlock()
	if show {
		fmt.Fprintf(os.Stderr, "%s %d%%\n", p.label, pct)
	}
}

func (p *Progress) Note(s string) {
	p.mu.Lock()
	p.note = s
	p.mu.Unlock()
}

func (p *Progress) Finish(err error) {
	p.mu.Lock()
	if p.finished {
		p.mu.Unlock()
		return
	}
	p.finished = true
	p.mu.Unlock()
	close(p.quit)
	<-p.done
	if p.tty {
		fmt.Fprint(os.Stderr, "\r\x1b[K")
	}
	d := Elapsed(time.Since(p.start))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %s %s\n", paint("31", "✘", p.color), p.label, paint("2", d, p.color))
		nlog.Printf("failed: %s: %v", p.label, err)
		return
	}
	fmt.Fprintf(os.Stderr, "%s %s %s\n", paint("32", "✔", p.color), p.label, paint("2", d, p.color))
	nlog.Printf("done: %s (%s)", p.label, d)
}

type Parser func(line string) (int, bool)

var (
	reCmake = regexp.MustCompile(`^\[\s*(\d{1,3})%\]`)
	reNinja = regexp.MustCompile(`^\[(\d+)/(\d+)\]`)
	reRecv  = regexp.MustCompile(`Receiving objects:\s+(\d{1,3})%`)
	reDelta = regexp.MustCompile(`Resolving deltas:\s+(\d{1,3})%`)
)

func Generic(line string) (int, bool) {
	if m := reCmake.FindStringSubmatch(line); m != nil {
		v, _ := strconv.Atoi(m[1])
		return v, true
	}
	if m := reNinja.FindStringSubmatch(line); m != nil {
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		if b > 0 {
			return a * 100 / b, true
		}
	}
	if m := reRecv.FindStringSubmatch(line); m != nil {
		v, _ := strconv.Atoi(m[1])
		return v * 70 / 100, true
	}
	if m := reDelta.FindStringSubmatch(line); m != nil {
		v, _ := strconv.Atoi(m[1])
		return 70 + v*30/100, true
	}
	return 0, false
}

func splitLines(data []byte, atEOF bool) (int, []byte, error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

type tailBuf struct {
	n     int
	lines []string
}

func (t *tailBuf) add(s string) {
	t.lines = append(t.lines, s)
	if len(t.lines) > t.n {
		t.lines = t.lines[len(t.lines)-t.n:]
	}
}

func RunCmd(label string, cmd *exec.Cmd, logw io.Writer, parse Parser) error {
	p := Start(label)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	tail := &tailBuf{n: 20}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		sc.Split(splitLines)
		for sc.Scan() {
			line := strings.TrimRight(sc.Text(), " \t")
			if strings.TrimSpace(line) == "" {
				continue
			}
			if logw != nil {
				fmt.Fprintln(logw, line)
			}
			clean := strings.TrimSpace(line)
			tail.add(clean)
			p.Note(clean)
			if parse != nil {
				if v, ok := parse(clean); ok {
					p.Set(v)
				}
			}
		}
		io.Copy(io.Discard, pr)
	}()
	err := cmd.Run()
	pw.Close()
	<-readDone
	p.Finish(err)
	if err != nil {
		for _, l := range tail.lines {
			fmt.Fprintln(os.Stderr, ed("  "+l))
		}
	}
	return err
}

type Reader struct {
	R     io.Reader
	Total int64
	N     int64
	P     *Progress
}

func (r *Reader) Read(b []byte) (int, error) {
	n, err := r.R.Read(b)
	r.N += int64(n)
	if r.P != nil {
		if r.Total > 0 {
			r.P.Set(int(r.N * 100 / r.Total))
			r.P.Note(HumanBytes(r.N) + " / " + HumanBytes(r.Total))
		} else {
			r.P.Note(HumanBytes(r.N))
		}
	}
	return n, err
}

var in = bufio.NewReader(os.Stdin)

func readLine() (string, bool) {
	s, err := in.ReadString('\n')
	if err != nil && s == "" {
		return "", false
	}
	return strings.TrimSpace(s), true
}

func Ask(q, def string) string {
	if def != "" {
		fmt.Fprintf(os.Stderr, "%s %s\n> ", eb(q), ed("["+def+"]"))
	} else {
		fmt.Fprintf(os.Stderr, "%s %s\n> ", eb(q), ed("(Enter to skip)"))
	}
	s, ok := readLine()
	if !ok || s == "" {
		return def
	}
	if s == "-" {
		return ""
	}
	return s
}

func Confirm(q string, def bool) bool {
	hint := "(y/N)"
	if def {
		hint = "(Y/n)"
	}
	fmt.Fprintf(os.Stderr, "%s %s ", eb(q), ed(hint))
	s, ok := readLine()
	if !ok {
		fmt.Fprintln(os.Stderr)
		return def
	}
	switch strings.ToLower(s) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	}
	return def
}

func Choose(q string, opts []string, def int) int {
	fmt.Fprintln(os.Stderr, eb(q))
	for i, o := range opts {
		mark := " "
		if i == def {
			mark = ec("*")
		}
		fmt.Fprintf(os.Stderr, " %s %d) %s\n", mark, i+1, o)
	}
	for try := 0; try < 4; try++ {
		fmt.Fprintf(os.Stderr, "%s ", ed(fmt.Sprintf("[%d]>", def+1)))
		s, ok := readLine()
		if !ok || s == "" {
			return def
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(opts) {
			return n - 1
		}
		for i, o := range opts {
			if strings.EqualFold(s, o) {
				return i
			}
		}
		fmt.Fprintln(os.Stderr, ey("choose a number from the list"))
	}
	return def
}

func ChooseMulti(q string, opts []string, defs []int) []int {
	fmt.Fprintln(os.Stderr, eb(q)+" "+ed("(comma separated, '-' for none)"))
	isDef := map[int]bool{}
	for _, d := range defs {
		isDef[d] = true
	}
	for i, o := range opts {
		mark := " "
		if isDef[i] {
			mark = ec("*")
		}
		fmt.Fprintf(os.Stderr, " %s %d) %s\n", mark, i+1, o)
	}
	for try := 0; try < 4; try++ {
		fmt.Fprint(os.Stderr, ed("> "))
		s, ok := readLine()
		if !ok || s == "" {
			return defs
		}
		if s == "-" || strings.EqualFold(s, "none") {
			return nil
		}
		var out []int
		seen := map[int]bool{}
		bad := false
		for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
			n, err := strconv.Atoi(f)
			if err != nil || n < 1 || n > len(opts) {
				bad = true
				break
			}
			if !seen[n-1] {
				seen[n-1] = true
				out = append(out, n-1)
			}
		}
		if !bad {
			return out
		}
		fmt.Fprintln(os.Stderr, ey("use numbers from the list"))
	}
	return defs
}

func Table(header []string, rows [][]string) {
	w := make([]int, len(header))
	for i, h := range header {
		w[i] = utf8.RuneCountInString(h)
	}
	for _, r := range rows {
		for i := 0; i < len(r) && i < len(w); i++ {
			if n := utf8.RuneCountInString(r[i]); n > w[i] {
				w[i] = n
			}
		}
	}
	line := func(cells []string, style func(string) string) {
		var b strings.Builder
		for i := range header {
			c := ""
			if i < len(cells) {
				c = cells[i]
			}
			if i == len(header)-1 {
				b.WriteString(style(c))
				break
			}
			b.WriteString(style(c))
			b.WriteString(strings.Repeat(" ", w[i]-utf8.RuneCountInString(c)+2))
		}
		fmt.Println(b.String())
	}
	line(header, Dim)
	for _, r := range rows {
		line(r, func(s string) string { return s })
	}
}
