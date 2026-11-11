// Package ui contains all human and machine output: messages, tables,
// prompts and progress. Keeping it in one place keeps commands small.
package ui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// UI writes user-facing output.
type UI struct {
	Out     io.Writer
	Err     io.Writer
	In      io.Reader
	JSON    bool
	Verbose bool
	Color   bool
}

// New returns a UI bound to the standard streams.
func New() *UI {
	noColor := os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb"
	return &UI{
		Out:   os.Stdout,
		Err:   os.Stderr,
		In:    os.Stdin,
		Color: !noColor,
	}
}

func (u *UI) paint(code, s string) string {
	if !u.Color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Human returns the writer for prose (stderr in JSON mode).
func (u *UI) Human() io.Writer {
	if u.JSON {
		return u.Err
	}
	return u.Out
}

// Infof prints a normal message.
func (u *UI) Infof(format string, a ...any) {
	fmt.Fprintf(u.Human(), format+"\n", a...)
}

// Successf prints a success message with a check mark.
func (u *UI) Successf(format string, a ...any) {
	fmt.Fprintf(u.Human(), u.paint("32", "✓ ")+format+"\n", a...)
}

// Warnf prints a warning.
func (u *UI) Warnf(format string, a ...any) {
	fmt.Fprintf(u.Human(), u.paint("33", "warning: ")+format+"\n", a...)
}

// Errorf prints an error.
func (u *UI) Errorf(format string, a ...any) {
	fmt.Fprintf(u.Err, u.paint("31", "error: ")+format+"\n", a...)
}

// Verbosef prints only when --verbose is set.
func (u *UI) Verbosef(format string, a ...any) {
	if u.Verbose {
		fmt.Fprintf(u.Human(), format+"\n", a...)
	}
}

// PrintJSON writes a stable, indented JSON document to stdout.
func (u *UI) PrintJSON(v any) error {
	enc := json.NewEncoder(u.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Table prints an aligned table. It returns nothing; rows are strings.
func (u *UI) Table(headers []string, rows [][]string) {
	w := u.Human()
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i := range headers {
			if i < len(r) && len(r[i]) > widths[i] {
				widths[i] = len(r[i])
			}
		}
	}
	var b strings.Builder
	for i, h := range headers {
		fmt.Fprintf(&b, "%-*s", widths[i]+2, h)
	}
	b.WriteString("\n")
	for _, r := range rows {
		for i := range headers {
			cell := ""
			if i < len(r) {
				cell = r[i]
			}
			fmt.Fprintf(&b, "%-*s", widths[i]+2, cell)
		}
		b.WriteString("\n")
	}
	fmt.Fprint(w, b.String())
}

// Confirm asks a yes/no question. defaultYes selects the [Y/n] form.
func (u *UI) Confirm(question string, defaultYes bool) (bool, error) {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	fmt.Fprintf(u.Human(), "%s %s ", question, suffix)
	line, err := u.readLine()
	if err != nil {
		return false, err
	}
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return defaultYes, nil
	}
	return line == "y" || line == "yes", nil
}

// ConfirmTyped requires the user to type an exact word.
func (u *UI) ConfirmTyped(question, expected string) (bool, error) {
	fmt.Fprintf(u.Human(), "%s\nType %q to confirm: ", question, expected)
	line, err := u.readLine()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(line) == expected, nil
}

// Prompt asks for a free-text value.
func (u *UI) Prompt(question, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(u.Human(), "%s [%s]: ", question, def)
	} else {
		fmt.Fprintf(u.Human(), "%s: ", question)
	}
	line, err := u.readLine()
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

func (u *UI) readLine() (string, error) {
	r := bufio.NewReader(u.In)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return line, nil
}

// Progress renders a single-line progress indicator on stderr.
type Progress struct {
	Err      io.Writer
	Label    string
	Total    int64
	Current  int64
	start    time.Time
	last     time.Time
	finished bool
}

// NewProgress creates a progress for a known total byte count.
func (u *UI) NewProgress(label string, total int64) *Progress {
	return &Progress{Err: u.Err, Label: label, Total: total, start: time.Now()}
}

// Add advances the progress by n bytes.
func (p *Progress) Add(n int64) {
	p.Current += n
	if time.Since(p.last) < 100*time.Millisecond && p.Current < p.Total {
		return
	}
	p.last = time.Now()
	p.render()
}

// Set sets the current byte count.
func (p *Progress) Set(n int64) { p.Current = n; p.last = time.Time{}; p.render() }

func (p *Progress) render() {
	if p.finished {
		return
	}
	elapsed := time.Since(p.start).Seconds()
	rate := 0.0
	if elapsed > 0 {
		rate = float64(p.Current) / elapsed
	}
	eta := "?"
	if rate > 0 && p.Total > p.Current {
		eta = (time.Duration(float64(p.Total-p.Current)/rate) * time.Second).Round(time.Second).String()
	}
	pct := 0.0
	if p.Total > 0 {
		pct = 100 * float64(p.Current) / float64(p.Total)
	}
	fmt.Fprintf(p.Err, "\r%s %5.1f%%  %s / %s  %s/s  ETA %s   ",
		p.Label, pct, FormatBytes(uint64(p.Current)), FormatBytes(uint64(p.Total)), FormatBytes(uint64(rate)), eta)
}

// Done finishes the progress line.
func (p *Progress) Done() {
	p.finished = true
	fmt.Fprintf(p.Err, "\r%s 100.0%%  %s / %s%s\n", p.Label,
		FormatBytes(uint64(p.Current)), FormatBytes(uint64(p.Total)), strings.Repeat(" ", 24))
}

// FormatBytes renders a byte count in human units.
func FormatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTP"[exp])
}
