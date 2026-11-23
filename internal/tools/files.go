package tools

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/paul-nameless/qi/internal/strutil"
)

const (
	maxSnapshotBytes = 256 << 10
	maxNoticeBytes   = 8 << 10
	diffContext      = 3
	maxDiffCells     = 1 << 20
)

type fileState struct {
	mod     time.Time
	text    string
	hasText bool
}

type fileTimes struct {
	mu   sync.Mutex
	seen map[string]fileState
}

func snapshot(path string) (fileState, bool) {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return fileState{}, false
	}
	s := fileState{mod: st.ModTime()}
	if st.Size() <= maxSnapshotBytes && ImageType(path) == "" {
		if data, err := os.ReadFile(path); err == nil && utf8.Valid(data) && bytes.IndexByte(data, 0) < 0 {
			s.text, s.hasText = string(data), true
		}
	}
	return s, true
}

func (f *fileTimes) remember(path string) {
	s, ok := snapshot(path)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen == nil {
		f.seen = map[string]fileState{}
	}
	f.seen[path] = s
}

func (f *fileTimes) forget() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = nil
}

func (f *fileTimes) check(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.seen[path]; ok && !st.ModTime().Equal(s.mod) {
		return fmt.Errorf("%s changed on disk since you last read or wrote it (by the user, a formatter or a command); read it again before changing it", path)
	}
	return nil
}

type fileChange struct {
	path   string
	notice string
}

const noDiffNotice = "Changed, no diff shown (binary or large file); read it again if you need the content: "

func (f *fileTimes) changes(rel func(string) string) []fileChange {
	f.mu.Lock()
	defer f.mu.Unlock()
	paths := make([]string, 0, len(f.seen))
	for p := range f.seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []fileChange
	for _, p := range paths {
		old := f.seen[p]
		st, err := os.Stat(p)
		if err != nil {
			delete(f.seen, p)
			out = append(out, fileChange{rel(p), rel(p) + " was deleted."})
			continue
		}
		if st.ModTime().Equal(old.mod) {
			continue
		}
		cur, ok := snapshot(p)
		if !ok {
			delete(f.seen, p)
			out = append(out, fileChange{rel(p), rel(p) + " is no longer a regular file."})
			continue
		}
		switch {
		case old.hasText && cur.hasText && old.text == cur.text:
		case old.hasText && cur.hasText:
			out = append(out, fileChange{rel(p), rel(p) + ":\n" + capNotice(unifiedDiff(old.text, cur.text))})
		default:
			out = append(out, fileChange{path: rel(p)})
		}
		f.seen[p] = cur
	}
	return out
}

func (e *Env) FileChanges() (string, []string) {
	changes := e.files.changes(e.rel)
	if len(changes) == 0 {
		return "", nil
	}
	paths := make([]string, len(changes))
	var notices, noDiff []string
	for i, c := range changes {
		paths[i] = c.path
		if c.notice == "" {
			noDiff = append(noDiff, c.path)
			continue
		}
		notices = append(notices, c.notice)
	}
	if len(noDiff) > 0 {
		notices = append(notices, noDiffNotice+strings.Join(noDiff, ", "))
	}
	return "Files you read or wrote earlier changed on disk since then (by the user, a formatter or a command). Diffs are against your last read or write; line numbers are current:\n\n" + strings.Join(notices, "\n\n"), paths
}

func (e *Env) ForgetFiles() {
	e.files.forget()
}

func capNotice(s string) string {
	if len(s) <= maxNoticeBytes {
		return s
	}
	return strutil.CutStart(s, maxNoticeBytes) + "\n… diff truncated; read the file for the rest"
}

type diffOp struct {
	kind   byte
	text   string
	oldNum int
	newNum int
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func unifiedDiff(oldText, newText string) string {
	ops := diffLines(splitLines(oldText), splitLines(newText))
	var b strings.Builder
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := max(i-diffContext, 0)
		end := i
		for j := i; j < len(ops) && j <= end+2*diffContext; j++ {
			if ops[j].kind != ' ' {
				end = j
			}
		}
		stop := min(end+diffContext+1, len(ops))
		writeHunk(&b, ops[start:stop])
		i = stop
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func writeHunk(b *strings.Builder, ops []diffOp) {
	oldStart, newStart, oldN, newN := 0, 0, 0, 0
	for _, op := range ops {
		if op.kind != '+' {
			if oldStart == 0 {
				oldStart = op.oldNum
			}
			oldN++
		}
		if op.kind != '-' {
			if newStart == 0 {
				newStart = op.newNum
			}
			newN++
		}
	}
	fmt.Fprintf(b, "@@ -%d,%d +%d,%d @@\n", oldStart, oldN, newStart, newN)
	for _, op := range ops {
		b.WriteByte(op.kind)
		b.WriteString(op.text)
		b.WriteByte('\n')
	}
}

func diffLines(a, b []string) []diffOp {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var ops []diffOp
	for i := 0; i < pre; i++ {
		ops = append(ops, diffOp{' ', a[i], i + 1, i + 1})
	}
	ops = append(ops, diffMiddle(a[pre:len(a)-suf], b[pre:len(b)-suf], pre)...)
	for i := 0; i < suf; i++ {
		ai, bi := len(a)-suf+i, len(b)-suf+i
		ops = append(ops, diffOp{' ', a[ai], ai + 1, bi + 1})
	}
	return ops
}

func diffMiddle(a, b []string, offset int) []diffOp {
	var ops []diffOp
	if len(a)*len(b) > maxDiffCells {
		for i, l := range a {
			ops = append(ops, diffOp{'-', l, offset + i + 1, 0})
		}
		for j, l := range b {
			ops = append(ops, diffOp{'+', l, 0, offset + j + 1})
		}
		return ops
	}
	lcs := make([][]int32, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int32, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i], offset + i + 1, offset + j + 1})
			i++
			j++
		case j < len(b) && (i == len(a) || lcs[i][j+1] > lcs[i+1][j]):
			ops = append(ops, diffOp{'+', b[j], 0, offset + j + 1})
			j++
		default:
			ops = append(ops, diffOp{'-', a[i], offset + i + 1, 0})
			i++
		}
	}
	return ops
}
