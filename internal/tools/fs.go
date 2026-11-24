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
	"time"
	"unicode/utf8"

	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/strutil"
)

const (
	maxLineLen   = 2000
	maxReadBytes = 20 << 20
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

func Read() *Function {
	return &Function{
		HookNames: []string{"Read"},
		Name:      "read",
		Path:      pathKey,
		Code: CodeSpec{
			Name:     "read",
			Params:   []string{"file_path"},
			Required: 1,
			Options:  []string{"offset", "limit", "crop"},
			Doc:      fmt.Sprintf("read(path, {offset, limit}) → string — file text without line numbers; offset is the 1-based first line, limit the number of lines (default: the whole file). A directory gives its entries, one per line. An image file (png, jpg, gif, webp) is attached to the result of this run call so you can see it (at most %d images per run call); the user does not see it, so to show an image to the user, put its full path in your answer. {crop: [x, y, width, height]} shows a region at full resolution.", maxCellImages),
		},
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
			if res, ok := readEmbedded(a.FilePath, a.Offset, a.Limit, a.Crop); ok {
				return res
			}
			path := env.resolve(a.FilePath)
			res, file := readFile(path, a.Offset, a.Limit, a.Crop)
			switch {
			case res.IsError:
			case file.data != nil:
				env.files.rememberData(path, file.data, file.mod)
			default:
				env.files.remember(path)
			}
			return res
		},
	}
}

type fileRead struct {
	data []byte
	mod  time.Time
}

func readFile(path string, offset, limit int, crop []int) (Result, fileRead) {
	st, err := os.Stat(path)
	if err != nil {
		return errResult("%v", err), fileRead{}
	}
	if st.IsDir() {
		return listDir(path), fileRead{}
	}
	if !st.Mode().IsRegular() {
		return errResult("%s is not a regular file", path), fileRead{}
	}
	if mt := ImageType(path); mt != "" {
		return readImage(path, mt, st.Size(), crop), fileRead{}
	}
	if len(crop) > 0 {
		return errResult("crop only applies to images"), fileRead{}
	}
	if st.Size() > maxReadBytes {
		return errResult("file is %d MB; use bash (head, sed -n, rg) to read parts of it", st.Size()>>20), fileRead{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errResult("%v", err), fileRead{}
	}
	return rawFile(data, offset, limit), fileRead{data, st.ModTime()}
}

func isBinary(data []byte) bool {
	probe := data
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	return bytes.IndexByte(probe, 0) >= 0
}

func rawFile(data []byte, offset, limit int) Result {
	switch {
	case isBinary(data):
		return errResult("binary file, %d bytes: read returns text only; use bash (file, xxd, strings) for it", len(data))
	case !utf8.Valid(data):
		return errResult("not valid UTF-8 text: read would alter its bytes; use bash (iconv, sed) for it")
	}
	return rawText(string(data), offset, limit)
}

func rawText(data string, offset, limit int) Result {
	if data == "" {
		return Result{Summary: "empty"}
	}
	lines := strings.SplitAfter(data, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if offset < 1 {
		offset = 1
	}
	if offset > len(lines) {
		return errResult("offset %d is past the end of the file (%d lines)", offset, len(lines))
	}
	end := len(lines)
	if limit > 0 {
		end = min(offset-1+limit, len(lines))
	}
	if offset == 1 && end == len(lines) {
		return Result{Text: data, Summary: fmt.Sprintf("%d lines", end)}
	}
	return Result{Text: strings.Join(lines[offset-1:end], ""), Summary: fmt.Sprintf("%d lines", end-offset+1)}
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

func Write() *Function {
	return &Function{
		HookNames: []string{"Write"},
		Name:      "write",
		Path:      pathKey,
		Label:     func(env *Env, in json.RawMessage) string { return "write " + env.rel(pathKey(env, in)) },
		Code: CodeSpec{
			Name:     "write",
			Params:   []string{"file_path", "content"},
			Required: 2,
			Doc:      "write(path, content) → string — create or overwrite a file; prefer edit for changes to existing files.",
		},
		Run: func(ctx context.Context, env *Env, in json.RawMessage, _ func(string)) Result {
			if env.readOnly() {
				return deniedResult("read-only mode: write is not allowed")
			}
			a, err := decode[writeArgs](in)
			if err != nil {
				return errResult("%v", err)
			}
			if res, ok := builtinWrite(a.FilePath); ok {
				return res
			}
			if err := env.checkWritable(a.FilePath); err != nil {
				return deniedResult("%v", err)
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
			env.files.rememberWritten(path, []byte(a.Content))
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

func Edit() *Function {
	return &Function{
		HookNames: []string{"Edit"},
		Name:      "edit",
		Path:      pathKey,
		Label:     func(env *Env, in json.RawMessage) string { return "edit " + env.rel(pathKey(env, in)) },
		Code: CodeSpec{
			Name:     "edit",
			Params:   []string{"file_path", "old_string", "new_string"},
			Required: 3,
			Options:  []string{"replace_all"},
			Alias:    map[string]string{"all": "replace_all"},
			Doc:      "edit(path, old, new, {all}) → string — replace old with new; old must match exactly once (include enough surrounding lines) unless all is true.",
		},
		Run: func(ctx context.Context, env *Env, in json.RawMessage, _ func(string)) Result {
			if env.readOnly() {
				return deniedResult("read-only mode: edit is not allowed")
			}
			a, err := decode[editArgs](in)
			if err != nil {
				return errResult("%v", err)
			}
			if res, ok := builtinWrite(a.FilePath); ok {
				return res
			}
			if err := env.checkWritable(a.FilePath); err != nil {
				return deniedResult("%v", err)
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
			env.files.rememberWritten(path, []byte(updated))
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

func lineDelta(oldText, newText string, count int) (int, int) {
	o := strings.Count(oldText, "\n") + 1
	n := strings.Count(newText, "\n") + 1
	if newText == "" {
		n = 0
	}
	return n * count, o * count
}

var lineNumPrefix = regexp.MustCompile(`^\s*\d+(\t|\| ?|: )`)

func applyEdit(content, oldText, newText string, all bool) (string, int, error) {
	if oldText == "" {
		return "", 0, errors.New("old is empty; use write to create a file")
	}
	if oldText == newText {
		return "", 0, errors.New("old and new are identical")
	}
	crlf := strings.Contains(content, "\r\n")
	if crlf && !strings.Contains(oldText, "\r\n") {
		oldText = strings.ReplaceAll(oldText, "\n", "\r\n")
		newText = strings.ReplaceAll(newText, "\n", "\r\n")
	}
	if out, n, err := replaceExact(content, oldText, newText, all); n != 0 || err != nil {
		return out, n, err
	}
	if o2, n2, ok := stripLineNumbers(oldText, newText); ok {
		if out, n, err := replaceExact(content, o2, n2, all); n != 0 || err != nil {
			return out, n, err
		}
	}
	out, n := replaceTrimmed(content, oldText, newText)
	switch {
	case n == 1:
		return out, 1, nil
	case n > 1:
		return "", 0, fmt.Errorf("old matches %d places when ignoring whitespace; include more context", n)
	}
	return "", 0, errors.New("old not found in file; read the file again and copy the text exactly")
}

func replaceExact(content, oldText, newText string, all bool) (string, int, error) {
	n := strings.Count(content, oldText)
	switch {
	case n == 0:
		return "", 0, nil
	case n == 1:
		return strings.Replace(content, oldText, newText, 1), 1, nil
	case all:
		return strings.ReplaceAll(content, oldText, newText), n, nil
	default:
		return "", 0, fmt.Errorf("old matches %d places; include more context or pass {all: true}", n)
	}
}

func stripLineNumbers(oldText, newText string) (string, string, bool) {
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
	o, ok := strip(oldText)
	if !ok {
		return oldText, newText, false
	}
	n, _ := strip(newText)
	return o, n, true
}

func replaceTrimmed(content, oldText, newText string) (string, int) {
	oldLines := strings.Split(strings.Trim(oldText, "\r\n"), "\n")
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
	if trimmed := strings.Trim(newText, "\r\n"); trimmed != "" {
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
			line = strutil.CutStart(line, maxLineLen) + "…"
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
