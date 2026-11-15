package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/paul-nameless/qi/internal/llm"
)

const (
	defaultReadLimit = 2000
	maxLineLen       = 2000
	maxReadBytes     = 20 << 20
	maxReadOutput    = 60 << 10
)

var imageTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

func ImageType(path string) string {
	return imageTypes[strings.ToLower(filepath.Ext(path))]
}

type readArgs struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
	Crop     []int  `json:"crop"`
}

func Read() *Tool {
	return &Tool{
		ParallelSafe: true,
		HookNames:    []string{"Read"},
		Def: llm.ToolDef{
			Name:        "read",
			Description: "Read a file with line numbers (`N<tab>line`), a directory listing, or an image. Reads up to 2000 lines; use offset/limit for more. Images larger than 1568px are scaled down; use crop to see a region at full resolution.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string","description":"Path to the file or directory"},"offset":{"type":"integer","description":"1-based line number to start from"},"limit":{"type":"integer","description":"Maximum number of lines to read"},"crop":{"type":"array","items":{"type":"integer"},"description":"Images only: [x, y, width, height] in original pixels; returns that region at full resolution (scaled down only if larger than 1568px)"}},"required":["file_path"]}`),
		},
		Path: pathKey,
		Label: func(env *Env, in json.RawMessage) string {
			a, _ := decode[readArgs](in)
			l := "read " + env.rel(env.resolve(a.FilePath))
			if a.Offset > 0 || a.Limit > 0 {
				l += fmt.Sprintf(":%d", max(a.Offset, 1))
				if a.Limit > 0 {
					l += fmt.Sprintf("+%d", a.Limit)
				}
			}
			return l
		},
		Run: func(ctx context.Context, env *Env, in json.RawMessage, _ func(string)) Result {
			a, err := decode[readArgs](in)
			if err != nil {
				return errResult("%v", err)
			}
			if a.FilePath == "" {
				return errResult("file_path is required")
			}
			path := env.resolve(a.FilePath)
			res := readPath(path, a.Offset, a.Limit, a.Crop)
			if !res.IsError {
				env.files.remember(path)
			}
			return res
		},
	}
}

func readPath(path string, offset, limit int, crop []int) Result {
	st, err := os.Stat(path)
	if err != nil {
		return errResult("%v", err)
	}
	if st.IsDir() {
		return listDir(path)
	}
	if !st.Mode().IsRegular() {
		return errResult("%s is not a regular file", path)
	}
	if mt := ImageType(path); mt != "" {
		return readImage(path, mt, st.Size(), crop)
	}
	if len(crop) > 0 {
		return errResult("crop only applies to images")
	}
	if st.Size() > maxReadBytes {
		return errResult("file is %d MB; use bash (head, sed -n, rg) to read parts of it", st.Size()>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errResult("%v", err)
	}
	probe := data
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	if bytes.IndexByte(probe, 0) >= 0 {
		return Result{Text: fmt.Sprintf("binary file, %d bytes", len(data)), Summary: "binary"}
	}
	if len(data) == 0 {
		return Result{Text: "(empty file)", Summary: "empty"}
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if offset < 1 {
		offset = 1
	}
	if limit <= 0 {
		limit = defaultReadLimit
	}
	if offset > len(lines) {
		return errResult("offset %d is past the end of the file (%d lines)", offset, len(lines))
	}
	end := min(offset-1+limit, len(lines))
	var b strings.Builder
	for i := offset - 1; i < end; i++ {
		if b.Len() >= maxReadOutput {
			end = i
			break
		}
		line := strings.TrimSuffix(lines[i], "\r")
		if len(line) > maxLineLen {
			line = cutStart(line, maxLineLen) + "…"
		}
		fmt.Fprintf(&b, "%d\t%s\n", i+1, line)
	}
	switch {
	case end < len(lines):
		fmt.Fprintf(&b, "(lines %d-%d of %d; continue with offset=%d)\n", offset, end, len(lines), end+1)
	case offset > 1:
		fmt.Fprintf(&b, "(lines %d-%d of %d)\n", offset, end, len(lines))
	}
	return Result{Text: b.String(), Summary: fmt.Sprintf("%d lines", end-offset+1)}
}

func listDir(path string) Result {
	entries, err := os.ReadDir(path)
	if err != nil {
		return errResult("%v", err)
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() {
			n += "/"
		}
		names = append(names, n)
	}
	sort.Strings(names)
	more := ""
	if len(names) > 500 {
		more = fmt.Sprintf("\n… %d more", len(names)-500)
		names = names[:500]
	}
	return Result{Text: strings.Join(names, "\n") + more, Summary: fmt.Sprintf("%d entries", len(entries))}
}

func readImage(path, mediaType string, size int64, crop []int) Result {
	img, err := loadImage(path, mediaType, size, crop)
	if err != nil {
		return errResult("%v", err)
	}
	return Result{Text: img.note, Images: []llm.Block{img.block}, Summary: "image"}
}

func loadImage(path, mediaType string, size int64, crop []int) (preparedImage, error) {
	if size > maxImageFile {
		return preparedImage{}, fmt.Errorf("image too large (%d MB, max %d MB)", size>>20, maxImageFile>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return preparedImage{}, err
	}
	return prepareImage(data, mediaType, path, crop)
}

func LoadImage(path string) (llm.Block, string, error) {
	mt := ImageType(path)
	if mt == "" {
		return llm.Block{}, "", errors.New("not an image")
	}
	st, err := os.Stat(path)
	if err != nil {
		return llm.Block{}, "", err
	}
	if !st.Mode().IsRegular() {
		return llm.Block{}, "", errors.New("not a regular file")
	}
	img, err := loadImage(path, mt, st.Size(), nil)
	if err != nil {
		return llm.Block{}, "", err
	}
	return img.block, img.note, nil
}

type writeArgs struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func pathKey(env *Env, in json.RawMessage) string {
	var a struct {
		FilePath string `json:"file_path"`
	}
	if json.Unmarshal(in, &a) != nil || a.FilePath == "" {
		return ""
	}
	return env.resolve(a.FilePath)
}

func Write() *Tool {
	return &Tool{
		Mutates:   true,
		HookNames: []string{"Write"},
		Def: llm.ToolDef{
			Name:        "write",
			Description: "Create or overwrite a file. Prefer edit for changes to existing files.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"content":{"type":"string"}},"required":["file_path","content"]}`),
		},
		Path:  pathKey,
		Label: func(env *Env, in json.RawMessage) string { return "write " + env.rel(pathKey(env, in)) },
		Run: func(ctx context.Context, env *Env, in json.RawMessage, _ func(string)) Result {
			if env.readOnly() {
				return errResult("read-only mode: write is not allowed")
			}
			a, err := decode[writeArgs](in)
			if err != nil {
				return errResult("%v", err)
			}
			if a.FilePath == "" {
				return errResult("file_path is required")
			}
			path := env.resolve(a.FilePath)
			_, statErr := os.Stat(path)
			if statErr == nil {
				if err := env.files.check(path); err != nil {
					return errResult("%v", err)
				}
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return errResult("%v", err)
			}
			if err := os.WriteFile(path, []byte(a.Content), 0o644); err != nil {
				return errResult("%v", err)
			}
			env.files.remember(path)
			n := strings.Count(a.Content, "\n")
			if a.Content != "" && !strings.HasSuffix(a.Content, "\n") {
				n++
			}
			verb := "updated"
			if statErr != nil {
				verb = "created"
			}
			return Result{Text: fmt.Sprintf("%s %s (%d lines)", verb, env.rel(path), n), Summary: fmt.Sprintf("%s, %d lines", verb, n)}
		},
	}
}

type editArgs struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

func Edit() *Tool {
	return &Tool{
		Mutates:   true,
		HookNames: []string{"Edit"},
		Def: llm.ToolDef{
			Name:        "edit",
			Description: "Replace old_string with new_string in a file. old_string must match exactly once (include enough surrounding lines) unless replace_all is true. Do not include the line-number prefixes from read.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"old_string":{"type":"string","description":"Exact text to replace"},"new_string":{"type":"string","description":"Replacement text"},"replace_all":{"type":"boolean","description":"Replace every occurrence"}},"required":["file_path","old_string","new_string"]}`),
		},
		Path:  pathKey,
		Label: func(env *Env, in json.RawMessage) string { return "edit " + env.rel(pathKey(env, in)) },
		Run: func(ctx context.Context, env *Env, in json.RawMessage, _ func(string)) Result {
			if env.readOnly() {
				return errResult("read-only mode: edit is not allowed")
			}
			a, err := decode[editArgs](in)
			if err != nil {
				return errResult("%v", err)
			}
			path := env.resolve(a.FilePath)
			if st, err := os.Stat(path); err != nil {
				return errResult("%v", err)
			} else if !st.Mode().IsRegular() || st.Size() > maxReadBytes {
				return errResult("%s is not a regular file under %d MB", path, maxReadBytes>>20)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return errResult("%v", err)
			}
			updated, count, err := applyEdit(string(data), a.OldString, a.NewString, a.ReplaceAll)
			if err != nil {
				return errResult("%v", err)
			}
			if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
				return errResult("%v", err)
			}
			env.files.remember(path)
			added, removed := lineDelta(a.OldString, a.NewString, count)
			summary := fmt.Sprintf("+%d -%d", added, removed)
			if count > 1 {
				summary += fmt.Sprintf(" (%d places)", count)
			}
			text := fmt.Sprintf("edited %s: %s\n%s", env.rel(path), summary, snippet(updated, a.NewString))
			return Result{Text: text, Summary: summary}
		},
	}
}

func lineDelta(old, new string, count int) (int, int) {
	o := strings.Count(old, "\n") + 1
	n := strings.Count(new, "\n") + 1
	if new == "" {
		n = 0
	}
	return n * count, o * count
}

var lineNumPrefix = regexp.MustCompile(`^\s*\d+(\t|\| ?|: )`)

func applyEdit(content, old, new string, all bool) (string, int, error) {
	if old == "" {
		return "", 0, errors.New("old_string is empty; use write to create a file")
	}
	if old == new {
		return "", 0, errors.New("old_string and new_string are identical")
	}
	crlf := strings.Contains(content, "\r\n")
	if crlf && !strings.Contains(old, "\r\n") {
		old = strings.ReplaceAll(old, "\n", "\r\n")
		new = strings.ReplaceAll(new, "\n", "\r\n")
	}
	if out, n, err := replaceExact(content, old, new, all); n != 0 || err != nil {
		return out, n, err
	}
	if o2, n2, ok := stripLineNumbers(old, new); ok {
		if out, n, err := replaceExact(content, o2, n2, all); n != 0 || err != nil {
			return out, n, err
		}
	}
	out, n := replaceTrimmed(content, old, new)
	switch {
	case n == 1:
		return out, 1, nil
	case n > 1:
		return "", 0, fmt.Errorf("old_string matches %d places when ignoring whitespace; include more context", n)
	}
	return "", 0, errors.New("old_string not found in file; read the file again and copy the text exactly")
}

func replaceExact(content, old, new string, all bool) (string, int, error) {
	n := strings.Count(content, old)
	switch {
	case n == 0:
		return "", 0, nil
	case n == 1:
		return strings.Replace(content, old, new, 1), 1, nil
	case all:
		return strings.ReplaceAll(content, old, new), n, nil
	default:
		return "", 0, fmt.Errorf("old_string matches %d places; include more context or set replace_all", n)
	}
}

func stripLineNumbers(old, new string) (string, string, bool) {
	strip := func(s string) (string, bool) {
		lines := strings.Split(s, "\n")
		for _, l := range lines {
			if l != "" && !lineNumPrefix.MatchString(l) {
				return s, false
			}
		}
		for i, l := range lines {
			lines[i] = lineNumPrefix.ReplaceAllString(l, "")
		}
		return strings.Join(lines, "\n"), true
	}
	o, ok := strip(old)
	if !ok {
		return old, new, false
	}
	n, _ := strip(new)
	return o, n, true
}

func replaceTrimmed(content, old, new string) (string, int) {
	oldLines := strings.Split(strings.Trim(old, "\r\n"), "\n")
	lines := strings.Split(content, "\n")
	match, count := -1, 0
	for i := 0; i+len(oldLines) <= len(lines); i++ {
		ok := true
		for j, ol := range oldLines {
			if strings.TrimSpace(lines[i+j]) != strings.TrimSpace(ol) {
				ok = false
				break
			}
		}
		if ok {
			match = i
			count++
		}
	}
	if count != 1 {
		return "", count
	}
	var repl []string
	if trimmed := strings.Trim(new, "\r\n"); trimmed != "" {
		repl = strings.Split(trimmed, "\n")
	}
	if strings.HasSuffix(lines[match+len(oldLines)-1], "\r") {
		for i, l := range repl {
			if !strings.HasSuffix(l, "\r") {
				repl[i] = l + "\r"
			}
		}
	}
	out := append(append(append([]string{}, lines[:match]...), repl...), lines[match+len(oldLines):]...)
	return strings.Join(out, "\n"), 1
}

const snippetContext = 3

func snippet(content, changed string) string {
	i := strings.Index(content, strings.ReplaceAll(changed, "\n", lineEnding(content)))
	if i < 0 || changed == "" {
		return ""
	}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	first := strings.Count(content[:i], "\n")
	last := first + strings.Count(changed, "\n")
	from, to := max(first-snippetContext, 0), min(last+snippetContext, len(lines)-1)
	if from > to {
		return ""
	}
	if to-from > 40 {
		return ""
	}
	var b strings.Builder
	for n := from; n <= to; n++ {
		line := strings.TrimSuffix(lines[n], "\r")
		if len(line) > maxLineLen {
			line = cutStart(line, maxLineLen) + "…"
		}
		fmt.Fprintf(&b, "%d\t%s\n", n+1, line)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func lineEnding(content string) string {
	if strings.Contains(content, "\r\n") {
		return "\r\n"
	}
	return "\n"
}
