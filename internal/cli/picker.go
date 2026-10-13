package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
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

// Picker lets the user choose one of lines with the keyboard. Terminals
// that support it implement it; tests script it.
type Picker interface {
	Pick(title string, lines []string) (index int, action PickAction, err error)
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
		case 'H':
			return "home", nil
		case 'F':
			return "end", nil
		}
		return "", nil
	}
	return string(rune(b)), nil
}

const pickHelp = "↑/↓ move · Enter show · o open in browser · c comment · q quit"

// Pick shows lines on the controlling terminal and lets the user choose
// with the arrow keys.
func (devTTY) Pick(title string, lines []string) (int, PickAction, error) {
	if len(lines) == 0 {
		return 0, PickQuit, errors.New("nothing to choose from")
	}
	f, err := open()
	if err != nil {
		return 0, PickQuit, err
	}
	defer func() { _ = f.Close() }()
	state, err := term.MakeRaw(int(f.Fd()))
	if err != nil {
		return 0, PickQuit, fmt.Errorf("%w (%w)", errNoTerminal, err)
	}
	defer func() {
		_, _ = io.WriteString(f, "\x1b[?25h")
		_ = term.Restore(int(f.Fd()), state)
	}()
	width, height, err := term.GetSize(int(f.Fd()))
	if err != nil || width < 20 || height < 5 {
		width, height = 80, 24
	}
	m := &pickModel{n: len(lines)}
	r := bufio.NewReader(f)
	for {
		_, _ = io.WriteString(f, renderPick(title, lines, m.cur, width, height-3))
		k, err := readKey(r)
		if err != nil {
			return 0, PickQuit, err
		}
		if action, done := m.key(k); done {
			_, _ = io.WriteString(f, "\x1b[2J\x1b[H")
			return m.cur, action, nil
		}
	}
}

// renderPick draws the list with the cursor, scrolled to keep it visible.
func renderPick(title string, lines []string, cur, width, rows int) string {
	var b strings.Builder
	b.WriteString("\x1b[?25l\x1b[2J\x1b[H")
	b.WriteString(clip(sanitize.String(title), width) + "\r\n")
	b.WriteString(clip(pickHelp, width) + "\r\n\r\n")
	rows = max(rows-2, 1)
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
