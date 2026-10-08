package sanitize

import "testing"

// u builds a string from code points, so no invisible characters appear
// literally in this file.
func u(runes ...rune) string { return string(runes) }

func TestString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want string
	}{
		{"plain", "Fix the login page", "Fix the login page"},
		{"tab and newline kept", "a\tb\nc", "a\tb\nc"},
		{"unicode text kept", "Gr" + u(0xfc) + "ße " + u(0x65e5, 0x672c), "Gr" + u(0xfc) + "ße " + u(0x65e5, 0x672c)},
		{"zero width joiner in emoji kept", u(0x1f469, 0x200d, 0x1f4bb), u(0x1f469, 0x200d, 0x1f4bb)},
		{"escape sequences", "\x1b[31mred\x1b[0m", "[31mred[0m"},
		{"osc title and bell", "\x1b]0;title\x07x", "]0;titlex"},
		{"carriage return", "ok\rfake", "okfake"},
		{"DEL and backspace", "a\x7fb\x08c", "abc"},
		{"C1 control", "a" + u(0x9b) + "b", "ab"},
		{"bidi override", "invoice" + u(0x202e) + "fdp.exe", "invoicefdp.exe"},
		{"bidi isolates", u(0x2066) + "x" + u(0x2069), "x"},
		{"zero width space, word joiner and BOM", "a" + u(0x200b) + "b" + u(0xfeff) + "c" + u(0x2060) + "d", "abcd"},
		{"tag characters", "hi" + u(0xe0041, 0xe0042), "hi"},
		{"invalid UTF-8 replaced", "a\xffb", "a" + u(0xfffd) + "b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := String(tt.in); got != tt.want {
				t.Errorf("String(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
