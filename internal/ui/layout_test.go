package ui

import (
	"reflect"
	"strings"
	"testing"
)

func TestLayoutRows(t *testing.T) {
	cases := []struct {
		name     string
		buf      string
		pos      int
		rows     []visualRow
		row, col int
	}{
		{"empty", "", 0, []visualRow{{0, 0, false}}, 0, 2},
		{"plain", "abc", 3, []visualRow{{0, 3, false}}, 0, 5},
		{"plain cursor mid", "abc", 1, []visualRow{{0, 3, false}}, 0, 3},
		{"newline", "ab\ncd", 4, []visualRow{{0, 2, false}, {3, 5, false}}, 1, 3},
		{"cursor before newline", "ab\ncd", 2, []visualRow{{0, 2, false}, {3, 5, false}}, 0, 4},
		{"trailing newline", "ab\n", 3, []visualRow{{0, 2, false}, {3, 3, false}}, 1, 2},
		{"empty line in the middle", "a\n\nb", 2, []visualRow{{0, 1, false}, {2, 2, false}, {3, 4, false}}, 1, 2},
		{"exact width", "abcdefgh", 8, []visualRow{{0, 8, false}, {8, 8, true}}, 1, 2},
		{"exact width cursor inside", "abcdefgh", 7, []visualRow{{0, 8, false}}, 0, 9},
		{"exact width then newline, cursor elsewhere", "abcdefgh\nx", 10, []visualRow{{0, 8, false}, {9, 10, false}}, 1, 3},
		{"exact width then newline", "abcdefgh\nx", 8, []visualRow{{0, 8, false}, {8, 8, true}, {9, 10, false}}, 1, 2},
		{"soft wrap", "abcdefghij", 10, []visualRow{{0, 8, false}, {8, 10, true}}, 1, 4},
		{"cursor at wrap boundary", "abcdefghij", 8, []visualRow{{0, 8, false}, {8, 10, true}}, 1, 2},
		{"two wraps", "abcdefghijklmnopq", 17, []visualRow{{0, 8, false}, {8, 16, true}, {16, 17, true}}, 2, 3},
		{"wide runes", "日本語日本", 5, []visualRow{{0, 4, false}, {4, 5, true}}, 1, 4},
		{"wide rune does not fit", "abcdefg日", 8, []visualRow{{0, 7, false}, {7, 8, true}}, 1, 4},
		{"tab is one cell", "a\tb", 3, []visualRow{{0, 3, false}}, 0, 5},
		{"combining mark stays", "abcdefgé", 9, []visualRow{{0, 9, false}, {9, 9, true}}, 1, 2},
	}
	for _, c := range cases {
		got := layoutRows(2, 10, []rune(c.buf), c.pos)
		if !reflect.DeepEqual(got.rows, c.rows) || got.row != c.row || got.col != c.col {
			t.Errorf("%s: rows %v row %d col %d, want %v row %d col %d", c.name, got.rows, got.row, got.col, c.rows, c.row, c.col)
		}
	}
}

func TestLayoutFrame(t *testing.T) {
	buf := []rune("ab\tc\ndefghijklm")
	l := layoutRows(2, 10, buf, len(buf))
	got := l.frame("❯ ", buf, 0, 10)
	want := Frame{Rows: []string{"❯ ab c", "  defghijk", "  lm"}, Row: 2, Col: 4}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("frame %+v, want %+v", got, want)
	}
}

func TestLayoutTallBuffer(t *testing.T) {
	buf := []rune(strings.Repeat("x\n", 9) + "x")
	l := layoutRows(2, 10, buf, 10)
	top := scrollTop(0, l.row, len(l.rows), 3)
	got := l.frame("❯ ", buf, top, 3)
	want := Frame{Rows: []string{"  x", "  x", "  x"}, Row: 2, Col: 2}
	if top != 3 || !reflect.DeepEqual(got, want) {
		t.Errorf("top %d frame %+v, want 3 %+v", top, got, want)
	}
	if got := l.frame("❯ ", buf, 0, 3); got.Rows[0] != "❯ x" || len(got.Rows) != 3 {
		t.Errorf("first window %q", got.Rows)
	}
}

func TestScrollTop(t *testing.T) {
	cases := []struct{ top, cursor, total, limit, want int }{
		{0, 5, 3, 10, 0},
		{0, 5, 20, 3, 3},
		{4, 5, 20, 3, 4},
		{4, 2, 20, 3, 2},
		{9, 19, 20, 3, 17},
		{18, 19, 20, 3, 17},
	}
	for _, c := range cases {
		if got := scrollTop(c.top, c.cursor, c.total, c.limit); got != c.want {
			t.Errorf("scrollTop(%d, %d, %d, %d) = %d, want %d", c.top, c.cursor, c.total, c.limit, got, c.want)
		}
	}
}

func TestEchoText(t *testing.T) {
	cases := []struct {
		prompt, buf, want string
	}{
		{"❯ ", "abc", "❯ abc"},
		{"❯ ", "a\nb", "❯ a\n  b"},
		{"❯ ", "a\n", "❯ a\n  "},
		{"❯ ", "abcdefghij", "❯ abcdefgh\n  ij"},
		{"❯ ", "abcdefgh\nx", "❯ abcdefgh\n  x"},
		{"\x1b[33mro\x1b[0m ❯ ", "a\nb", "\x1b[33mro\x1b[0m ❯ a\n     b"},
	}
	for _, c := range cases {
		if got := echoText(c.prompt, 10, []rune(c.buf)); got != c.want {
			t.Errorf("echoText(%q, %q) = %q, want %q", c.prompt, c.buf, got, c.want)
		}
	}
}

func TestMoveVertical(t *testing.T) {
	cases := []struct {
		name           string
		buf            string
		pos, dir, goal int
		want, wantGoal int
		ok             bool
	}{
		{"up clips to a shorter row", "abc\ndefgh", 7, -1, -1, 3, 3, true},
		{"down keeps the column", "abc\ndefgh", 1, 1, -1, 5, 1, true},
		{"up at the first row", "abc\ndefgh", 2, -1, -1, 2, -1, false},
		{"down at the last row", "abc\ndefgh", 6, 1, -1, 6, -1, false},
		{"up across a soft wrap", "abcdefghij", 9, -1, -1, 1, 1, true},
		{"down across a soft wrap", "abcdefghij", 1, 1, -1, 9, 1, true},
		{"up from the row after a full line", "abcdefgh", 8, -1, -1, 0, 0, true},
		{"soft row end stays on its row", "xxxxxxx日yy\n0123456", 9, -1, 7, 6, 7, true},
		{"last row of a line reaches its end", "xxxxxxx日yy\n0123456", 18, -1, -1, 10, 7, true},
		{"wide runes", "日本\nabcd", 6, -1, -1, 1, 3, true},
		{"goal column survives a short row", "abcdef\na\nabcdef", 8, 1, 5, 14, 5, true},
		{"empty middle row", "abc\n\nabc", 2, 1, -1, 4, 2, true},
	}
	for _, c := range cases {
		pos, goal, ok := moveVertical(2, 10, []rune(c.buf), c.pos, c.dir, c.goal)
		if pos != c.want || goal != c.wantGoal || ok != c.ok {
			t.Errorf("%s: pos %d goal %d ok %v, want %d %d %v", c.name, pos, goal, ok, c.want, c.wantGoal, c.ok)
		}
	}
}

func TestShownFrameAfterReflow(t *testing.T) {
	s := shownFrame{widths: []int{10, 10, 4}, row: 2, col: 4, term: 10}
	cases := []struct{ width, above, height int }{{10, 2, 3}, {20, 2, 3}, {5, 4, 5}, {3, 9, 10}, {4, 6, 7}, {2, 11, 12}}
	for _, c := range cases {
		if got, h := s.above(c.width), s.height(c.width); got != c.above || h != c.height {
			t.Errorf("width %d: above %d height %d, want %d %d", c.width, got, h, c.above, c.height)
		}
	}
}
