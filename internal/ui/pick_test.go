package ui

import (
	"strings"
	"testing"
	"time"
)

func pickEditor(t *testing.T, keys ...Key) *Editor {
	e := testEditor(t)
	e.T.Keys = make(chan Key, len(keys)+1)
	e.Interrupt = make(chan struct{}, 1)
	for _, k := range keys {
		e.T.Keys <- k
	}
	return e
}

func typed(s string) []Key {
	var keys []Key
	for _, r := range s {
		keys = append(keys, Key{Kind: KeyRune, Rune: r})
	}
	return keys
}

func items(names ...string) []PickItem {
	out := make([]PickItem, len(names))
	for i, n := range names {
		out[i] = PickItem{Name: n}
	}
	return out
}

func TestFuzzy(t *testing.T) {
	cases := []struct {
		query, text string
		ok          bool
	}{
		{"", "anything", true},
		{"cfg", "config", true},
		{"pckgo", "internal/ui/pick.go", true},
		{"gfc", "config", false},
		{"Cfg", "config", false},
		{"Cfg", "Config", true},
		{"ая", "дача ая", true},
	}
	for _, c := range cases {
		if _, _, ok := fuzzy(c.query, c.text); ok != c.ok {
			t.Errorf("fuzzy(%q, %q) = %v", c.query, c.text, ok)
		}
	}
	start, _, _ := fuzzy("con", "config")
	middle, _, _ := fuzzy("con", "xxconfig")
	if start <= middle {
		t.Errorf("a match at the start should rank higher: %d <= %d", start, middle)
	}
}

func TestPickFiltersAndRanks(t *testing.T) {
	keys := append(typed("cfg"), Key{Kind: KeyEnter})
	e := pickEditor(t, keys...)
	i, ok := e.Pick(PickList{Prompt: "/", Items: items("help", "clear", "config", "context")}, PickOptions{})
	if !ok || i != 2 {
		t.Errorf("got %d %v, want config (2)", i, ok)
	}
}

func TestPickMovesAndCancels(t *testing.T) {
	e := pickEditor(t, Key{Kind: KeyDown}, Key{Kind: KeyDown}, Key{Kind: KeyUp}, Key{Kind: KeyEnter})
	if i, ok := e.Pick(PickList{Items: items("a", "b", "c")}, PickOptions{}); !ok || i != 1 {
		t.Errorf("got %d %v, want 1", i, ok)
	}
	e = pickEditor(t, Key{Kind: KeyEsc})
	if _, ok := e.Pick(PickList{Items: items("a")}, PickOptions{}); ok {
		t.Error("esc should cancel")
	}
	e = pickEditor(t)
	go func() {
		time.Sleep(20 * time.Millisecond)
		e.Interrupt <- struct{}{}
	}()
	if _, ok := e.Pick(PickList{Items: items("a")}, PickOptions{}); ok {
		t.Error("ctrl+c should cancel")
	}
	e = pickEditor(t, append(typed("zz"), Key{Kind: KeyEnter}, Key{Kind: KeyKillLine}, Key{Kind: KeyEnter})...)
	if i, ok := e.Pick(PickList{Items: items("a", "b")}, PickOptions{}); !ok || i != 0 {
		t.Errorf("enter without a match should wait: %d %v", i, ok)
	}
	if _, ok := pickEditor(t).Pick(PickList{}, PickOptions{}); ok {
		t.Error("no items should not open")
	}
}

func TestPickStaysOpenAndNavigates(t *testing.T) {
	var picked []int
	e := pickEditor(t, Key{Kind: KeyEnter}, Key{Kind: KeyDown}, Key{Kind: KeyEnter}, Key{Kind: KeyEsc})
	e.Pick(PickList{Prompt: "config ", Items: items("a", "b")}, PickOptions{OnPick: func(i int) *PickList {
		picked = append(picked, i)
		return &PickList{Prompt: "config ", Items: items("a", "b")}
	}})
	if len(picked) != 2 || picked[0] != 0 || picked[1] != 1 {
		t.Errorf("picked %v, want [0 1]: the cursor stays when the prompt is unchanged", picked)
	}

	dir := "top"
	list := func() *PickList {
		if dir == "top" {
			return &PickList{Prompt: "@ ", Items: items("sub/", "file")}
		}
		return &PickList{Prompt: "@ sub/", Items: items("inner")}
	}
	keys := append(typed("su"), Key{Kind: KeyEnter}, Key{Kind: KeyBackspace}, Key{Kind: KeyDown}, Key{Kind: KeyEnter})
	e = pickEditor(t, keys...)
	i, ok := e.Pick(*list(), PickOptions{
		OnPick: func(i int) *PickList {
			if dir == "top" && i == 0 {
				dir = "sub"
				return list()
			}
			return nil
		},
		OnBack: func() *PickList {
			dir = "top"
			return list()
		},
	})
	if !ok || i != 1 || dir != "top" {
		t.Errorf("got %d %v in %s, want file (1) after going down and back up", i, ok, dir)
	}

	e = pickEditor(t, Key{Kind: KeyTab})
	opened := false
	if i, ok := e.Pick(PickList{Items: items("sub/")}, PickOptions{OnPick: func(int) *PickList { opened = true; return nil }}); !ok || i != 0 || opened {
		t.Errorf("tab picks without OnPick: %d %v %v", i, ok, opened)
	}
}

func TestPickFrame(t *testing.T) {
	p := &Printer{TTY: false}
	st := &pickState{}
	st.show(&PickList{Prompt: "/", Items: []PickItem{{Name: "help", Desc: "show commands"}, {Name: "config", Desc: "settings"}}})
	st.query = []rune("c")
	st.filter()
	f := st.frame(p, 40, 20)
	want := []string{"/c", "  1/2", "▌ config  settings"}
	if strings.Join(f.Rows, "\n") != strings.Join(want, "\n") || f.Row != 0 || f.Col != 2 {
		t.Errorf("frame %q at %d,%d", f.Rows, f.Row, f.Col)
	}
	st.show(&PickList{Prompt: "/", Items: items("a", "b", "c", "d", "e", "f", "g", "h", "i", "j")})
	st.query = nil
	st.filter()
	for range 9 {
		st.key(Key{Kind: KeyDown})
	}
	f = st.frame(p, 40, 10)
	if last := f.Rows[len(f.Rows)-1]; last != "▌ j" || len(f.Rows) != 6 {
		t.Errorf("scrolled frame %q", f.Rows)
	}
}
