package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/nyactl/approvekit/sanitize"
)

// PickAction is what the user chose to do with the selected entry.
type PickAction int

const (
	PickQuit PickAction = iota
	PickShow
	PickOpen
	PickComment
)

// Picker lets the user choose one of lines with the keyboard and read
// longer text full screen. Terminals that support it implement it; tests
// script it.
type Picker interface {
	// Pick shows lines with the cursor at cursor and status below them,
	// and returns the chosen line and action.
	Pick(title string, lines []string, cursor int, status string) (index int, action PickAction, err error)
	// View shows text full screen until the user goes back.
	View(title, text string) error
}

// pickModel is the keyboard logic of the picker, apart from the terminal.
type pickModel struct {
	n, cur int
}

// key handles one key and reports whether the choice is made.
func (m *pickModel) key(k string) (PickAction, bool) {
	switch k {
	case "up", "k":
		if m.cur > 0 {
			m.cur--
		}
	case "down", "j":
		if m.cur < m.n-1 {
			m.cur++
		}
	case "home", "g":
		m.cur = 0
	case "end", "G":
		m.cur = m.n - 1
	case "enter":
		return PickShow, true
	case "o":
		return PickOpen, true
	case "c":
		return PickComment, true
	case "q", "ctrl-c", "esc":
		return PickQuit, true
	}
	return PickQuit, false
}

// readKey reads one key press from a terminal in raw mode.
func readKey(r *bufio.Reader) (string, error) {
	b, err := r.ReadByte()
	if err != nil {
		return "", err
	}
	switch b {
	case '\r', '\n':
		return "enter", nil
	case 3:
		return "ctrl-c", nil
	case 0x1b:
		next, err := r.ReadByte()
		if err != nil || (next != '[' && next != 'O') {
			return "esc", nil
		}
		code, err := r.ReadByte()
		if err != nil {
			return "esc", nil
		}
		switch code {
		case 'A':
			return "up", nil
		case 'B':
			return "down", nil
		case 'D':
			return "left", nil
		case '5', '6':
			// Page Up and Page Down end with a tilde.
			if t, err := r.ReadByte(); err == nil && t == '~' {
				if code == '5' {
					return "pgup", nil
				}
				return "pgdn", nil
			}
			return "", nil
		case 'H':
			return "home", nil
		case 'F':
			return "end", nil
		}
		return "", nil
	}
	return string(rune(b)), nil
}

const (
	pickHelp = "↑/↓ move · Enter show · o open in browser · c comment · q quit"
	viewHelp = "↑/↓ PgUp/PgDn Space scroll · Enter/q/Esc/← back to the list"
)

// rawTerminal opens the controlling terminal in raw mode; restore puts it
// back and clears the screen.
func rawTerminal() (f *os.File, width, height int, restore func(), err error) {
	f, err = open()
	if err != nil {
		return nil, 0, 0, nil, err
	}
	state, err := term.MakeRaw(int(f.Fd()))
	if err != nil {
		_ = f.Close()
		return nil, 0, 0, nil, fmt.Errorf("%w (%w)", errNoTerminal, err)
	}
	width, height, err = term.GetSize(int(f.Fd()))
	if err != nil || width < 20 || height < 5 {
		width, height = 80, 24
	}
	return f, width, height, func() {
		_, _ = io.WriteString(f, "\x1b[2J\x1b[H\x1b[?25h")
		_ = term.Restore(int(f.Fd()), state)
		_ = f.Close()
	}, nil
}

// Pick shows lines on the controlling terminal and lets the user choose
// with the arrow keys.
func (devTTY) Pick(title string, lines []string, cursor int, status string) (int, PickAction, error) {
	if len(lines) == 0 {
		return 0, PickQuit, errors.New("nothing to choose from")
	}
	f, width, height, restore, err := rawTerminal()
	if err != nil {
		return 0, PickQuit, err
	}
	defer restore()
	m := &pickModel{n: len(lines), cur: min(max(cursor, 0), len(lines)-1)}
	r := bufio.NewReader(f)
	for {
		_, _ = io.WriteString(f, renderPick(title, lines, m.cur, width, height-3, status))
		k, err := readKey(r)
		if err != nil {
			return 0, PickQuit, err
		}
		if action, done := m.key(k); done {
			return m.cur, action, nil
		}
	}
}

// View shows text full screen, scrollable, until the user goes back.
func (devTTY) View(title, text string) error {
	f, width, height, restore, err := rawTerminal()
	if err != nil {
		return err
	}
	defer restore()
	lines := wrap(sanitize.String(text), width)
	v := &viewModel{n: len(lines), rows: max(height-3, 1)}
	r := bufio.NewReader(f)
	for {
		_, _ = io.WriteString(f, renderView(title, lines, v.top, width, v.rows))
		k, err := readKey(r)
		if err != nil {
			return err
		}
		if v.key(k) {
			return nil
		}
	}
}

// viewModel scrolls text of n lines in a window of rows lines.
type viewModel struct {
	n, rows, top int
}

// key handles one key and reports whether to go back.
func (v *viewModel) key(k string) bool {
	last := max(v.n-v.rows, 0)
	switch k {
	case "up", "k":
		v.top--
	case "down", "j":
		v.top++
	case "pgup", "b":
		v.top -= v.rows
	case "pgdn", " ":
		v.top += v.rows
	case "home", "g":
		v.top = 0
	case "end", "G":
		v.top = last
	case "enter", "q", "esc", "left", "ctrl-c":
		return true
	}
	v.top = min(max(v.top, 0), last)
	return false
}

func renderView(title string, lines []string, top, width, rows int) string {
	var b strings.Builder
	b.WriteString("\x1b[?25l\x1b[2J\x1b[H")
	b.WriteString(clip(sanitize.String(title), width) + "\r\n")
	end := min(top+rows, len(lines))
	pos := fmt.Sprintf("%s  (lines %d-%d of %d)", viewHelp, min(top+1, end), end, len(lines))
	b.WriteString(clip(pos, width) + "\r\n")
	for _, l := range lines[top:end] {
		b.WriteString(l + "\r\n")
	}
	return b.String()
}

// wrap breaks text into lines of at most width runes.
func wrap(text string, width int) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		r := []rune(l)
		for len(r) > width {
			out = append(out, string(r[:width]))
			r = r[width:]
		}
		out = append(out, string(r))
	}
	return out
}

// renderPick draws the list with the cursor, scrolled to keep it visible,
// and a status line below it.
func renderPick(title string, lines []string, cur, width, rows int, status string) string {
	var b strings.Builder
	b.WriteString("\x1b[?25l\x1b[2J\x1b[H")
	b.WriteString(clip(sanitize.String(title), width) + "\r\n")
	b.WriteString(clip(pickHelp, width) + "\r\n\r\n")
	rows = max(rows-3, 1)
	start := max(0, min(cur-rows/2, len(lines)-rows))
	for i := start; i < len(lines) && i < start+rows; i++ {
		prefix := "  "
		if i == cur {
			prefix = "> "
		}
		line := clip(prefix+sanitize.String(lines[i]), width)
		if i == cur {
			line = "\x1b[7m" + line + "\x1b[0m"
		}
		b.WriteString(line + "\r\n")
	}
	if status != "" {
		b.WriteString("\r\n" + clip(sanitize.String(status), width) + "\r\n")
	}
	return b.String()
}

// clip shortens s to width runes, with an ellipsis.
func clip(s string, width int) string {
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	r := []rune(s)
	return string(r[:max(width-1, 0)]) + "…"
}
