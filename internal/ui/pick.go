package ui

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type PickItem struct {
	Name  string
	Desc  string
	Color string
}

type PickList struct {
	Prompt string
	Items  []PickItem
}

type PickOptions struct {
	SearchDesc bool
	OnPick     func(i int) *PickList
	OnBack     func() *PickList
}

type match struct {
	idx   int
	score int
	pos   []int
}

type pickState struct {
	prompt string
	items  []PickItem
	opts   PickOptions
	query  []rune
	found  []match
	cur    int
	top    int
	nameW  int
}

const pickNameMax = 24

func (e *Editor) Pick(list PickList, opts PickOptions) (int, bool) {
	if len(list.Items) == 0 && opts.OnBack == nil {
		return -1, false
	}
	st := &pickState{opts: opts}
	st.show(&list)
	old, oldFinal := e.P.SwapPrompt(func(width, height int) Frame { return st.frame(e.P, width, height) }, nil)
	defer e.P.SwapPrompt(old, oldFinal)
	drain(e.Interrupt)
	for {
		var k Key
		select {
		case <-e.Interrupt:
			return -1, false
		case k = <-e.T.Keys:
		}
		switch k.Kind {
		case KeyEsc, KeyEOF:
			return -1, false
		case KeyEnter, KeyTab:
			i, ok := st.selected()
			if !ok {
				continue
			}
			if k.Kind == KeyTab || st.opts.OnPick == nil {
				return i, true
			}
			next := st.opts.OnPick(i)
			if next == nil {
				return i, true
			}
			e.P.Locked(func() { st.show(next) })
		case KeyLeft, KeyBackspace:
			if st.opts.OnBack == nil || (k.Kind == KeyBackspace && len(st.query) > 0) {
				e.P.Locked(func() { st.key(k) })
				break
			}
			if next := st.opts.OnBack(); next != nil {
				e.P.Locked(func() { st.show(next) })
			}
		default:
			e.P.Locked(func() { st.key(k) })
		}
		e.P.RenderPrompt()
	}
}

func (st *pickState) show(list *PickList) {
	if list.Prompt != st.prompt {
		st.prompt, st.query, st.cur, st.top = list.Prompt, nil, 0, 0
	}
	items := list.Items
	st.items = items
	st.nameW = 0
	for _, it := range items {
		st.nameW = max(st.nameW, min(displayWidth(it.Name), pickNameMax))
	}
	st.filter()
}

func (st *pickState) selected() (int, bool) {
	if st.cur < 0 || st.cur >= len(st.found) {
		return -1, false
	}
	return st.found[st.cur].idx, true
}

func (st *pickState) key(k Key) {
	switch k.Kind {
	case KeyRune:
		st.query = append(st.query, k.Rune)
		st.cur, st.top = 0, 0
	case KeyPaste:
		st.query = append(st.query, []rune(strings.Join(strings.Fields(k.Text), " "))...)
		st.cur, st.top = 0, 0
	case KeyBackspace:
		if len(st.query) > 0 {
			st.query = st.query[:len(st.query)-1]
			st.cur, st.top = 0, 0
		}
	case KeyKillLine:
		st.query, st.cur, st.top = nil, 0, 0
	case KeyKillWord:
		st.query = st.query[:wordLeft(st.query, len(st.query))]
		st.cur, st.top = 0, 0
	case KeyUp:
		st.cur = max(st.cur-1, 0)
		return
	case KeyDown:
		st.cur = min(st.cur+1, len(st.found)-1)
		return
	default:
		return
	}
	st.filter()
}

func (st *pickState) filter() {
	q := string(st.query)
	st.found = st.found[:0]
	for i, it := range st.items {
		text := it.Name
		if st.opts.SearchDesc && it.Desc != "" {
			text += " " + it.Desc
		}
		if score, pos, ok := fuzzy(q, text); ok {
			st.found = append(st.found, match{idx: i, score: score, pos: pos})
		}
	}
	if q != "" {
		sort.SliceStable(st.found, func(a, b int) bool { return st.found[a].score > st.found[b].score })
	}
	st.cur = min(st.cur, max(len(st.found)-1, 0))
}

func fuzzy(query, text string) (int, []int, bool) {
	if query == "" {
		return 0, nil, true
	}
	fold := !strings.ContainsFunc(query, unicode.IsUpper)
	q := []rune(query)
	t := []rune(text)
	pos := make([]int, 0, len(q))
	score, qi, prev := 0, 0, -2
	for ti := 0; ti < len(t) && qi < len(q); ti++ {
		a, b := t[ti], q[qi]
		if fold {
			a = unicode.ToLower(a)
		}
		if a != b {
			continue
		}
		switch {
		case ti == prev+1:
			score += 8
		case ti == 0 || !isWord(t[ti-1]):
			score += 6
		default:
			score -= min(ti-prev, 5)
		}
		pos = append(pos, ti)
		prev = ti
		qi++
	}
	if qi < len(q) {
		return 0, nil, false
	}
	return score - len(t)/8, pos, true
}

func (st *pickState) frame(p *Printer, width, height int) Frame {
	list := max(min(len(st.found), height*2/5), min(len(st.found), 3))
	if st.cur < st.top {
		st.top = st.cur
	}
	if st.cur >= st.top+list {
		st.top = st.cur - list + 1
	}
	head := st.prompt + string(st.query)
	rows := []string{truncateVisible(head, width-1)}
	rows = append(rows, p.Color(Dim, "  "+strconv.Itoa(len(st.found))+"/"+strconv.Itoa(len(st.items))))
	for i := st.top; i < st.top+list && i < len(st.found); i++ {
		rows = append(rows, truncateVisible(st.row(p, st.found[i], i == st.cur), width-1))
	}
	return Frame{Rows: rows, Row: 0, Col: min(VisibleLen(head), width-1)}
}

func (st *pickState) row(p *Printer, m match, current bool) string {
	it := st.items[m.idx]
	hit := map[int]bool{}
	for _, i := range m.pos {
		hit[i] = true
	}
	var b strings.Builder
	n := 0
	for _, r := range it.Name {
		s := string(r)
		if hit[n] {
			s = p.Color(Green, s)
		}
		b.WriteString(s)
		n++
	}
	name := b.String()
	if it.Color != "" {
		name = it.Color + strings.ReplaceAll(name, Reset, Reset+it.Color) + Reset
	}
	name += strings.Repeat(" ", max(st.nameW-displayWidth(it.Name), 0))
	mark := "  "
	if current {
		mark = p.Color(Cyan, "▌ ")
		name = p.Color(Bold, name)
	}
	if it.Desc == "" {
		return mark + name
	}
	return mark + name + "  " + p.Color(Dim, Sanitize(it.Desc))
}
