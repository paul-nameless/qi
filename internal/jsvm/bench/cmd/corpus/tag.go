package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"

	"github.com/paul-nameless/qi/internal/jsvm/bench/harness"
	"github.com/paul-nameless/qi/internal/jsvm/bench/oracle"
)

var identTags = map[string]string{
	"Symbol": "Symbol", "Date": "Date", "WeakMap": "WeakMap", "WeakSet": "WeakMap", "WeakRef": "WeakRef", "FinalizationRegistry": "WeakRef",
	"BigInt": "BigInt", "arguments": "arguments", "Proxy": "Proxy", "Reflect": "Reflect", "eval": "eval", "Intl": "Intl",
	"Atomics": "Atomics", "SharedArrayBuffer": "Atomics", "ArrayBuffer": "typed-array", "DataView": "typed-array",
	"Uint8Array": "typed-array", "Int8Array": "typed-array", "Uint16Array": "typed-array", "Int16Array": "typed-array",
	"Uint32Array": "typed-array", "Int32Array": "typed-array", "Float32Array": "typed-array", "Float64Array": "typed-array",
	"Uint8ClampedArray": "typed-array", "BigInt64Array": "typed-array", "BigUint64Array": "typed-array", "Function": "Function-ctor",
	"TextEncoder": "TextEncoder", "TextDecoder": "TextEncoder", "URL": "URL", "atob": "base64", "btoa": "base64",
}

var builtinCtors = map[string]bool{
	"Map": true, "Set": true, "Error": true, "TypeError": true, "RangeError": true, "SyntaxError": true, "ReferenceError": true,
	"EvalError": true, "URIError": true, "AggregateError": true, "Array": true, "Object": true, "Promise": true, "RegExp": true,
	"String": true, "Number": true, "Boolean": true, "Date": true, "WeakMap": true, "WeakSet": true, "WeakRef": true, "Proxy": true,
	"Function": true, "ArrayBuffer": true, "DataView": true, "Uint8Array": true, "Int8Array": true, "Uint16Array": true, "Int16Array": true,
	"Uint32Array": true, "Int32Array": true, "Float32Array": true, "Float64Array": true, "Uint8ClampedArray": true, "TextEncoder": true,
	"TextDecoder": true, "URL": true, "Intl": true, "FinalizationRegistry": true, "SharedArrayBuffer": true, "BigInt64Array": true, "BigUint64Array": true,
}

var (
	escRe       = regexp.MustCompile(`\\.`)
	backrefRe   = regexp.MustCompile(`\\[1-9]|\\k<`)
	namedRe     = regexp.MustCompile(`\(\?<[A-Za-z_$]`)
	propEscRe   = regexp.MustCompile(`\\[pP]\{`)
	astralEscRe = regexp.MustCompile(`\\u\{`)
)

type tagger struct{ tags map[string]bool }

func (t *tagger) add(s string) { t.tags[s] = true }

func (t *tagger) regex(pattern, flags string) {
	if backrefRe.MatchString(pattern) {
		t.add("regex-backref")
	}
	if propEscRe.MatchString(pattern) || astralEscRe.MatchString(pattern) {
		t.add("regex-unicode-escape")
	}
	plain := escRe.ReplaceAllString(pattern, "__")
	if strings.Contains(plain, "(?=") || strings.Contains(plain, "(?!") {
		t.add("regex-lookahead")
	}
	if strings.Contains(plain, "(?<=") || strings.Contains(plain, "(?<!") {
		t.add("regex-lookbehind")
	}
	if namedRe.MatchString(plain) {
		t.add("regex-named-group")
	}
	for _, f := range "ysudv" {
		if strings.ContainsRune(flags, f) {
			t.add("regex-flag-" + string(f))
		}
	}
}

func (t *tagger) regexpCall(callee ast.Expression, args []ast.Expression) {
	id, ok := callee.(*ast.Identifier)
	if !ok || id.Name != "RegExp" || len(args) == 0 {
		return
	}
	if s, ok := args[0].(*ast.StringLiteral); ok {
		flags := ""
		if len(args) > 1 {
			if f, ok := args[1].(*ast.StringLiteral); ok {
				flags = string(f.Value)
			}
		}
		t.regex(string(s.Value), flags)
	}
}

func (t *tagger) node(n any) bool {
	switch x := n.(type) {
	case *ast.ClassLiteral, *ast.ClassStaticBlock, *ast.SuperExpression:
		t.add("class")
	case *ast.PrivateIdentifier, *ast.PrivateDotExpression:
		t.add("class-private")
	case *ast.FunctionLiteral:
		if x.Generator {
			t.add("generator")
		}
		if x.Generator && x.Async {
			t.add("async-generator")
		}
	case *ast.YieldExpression:
		t.add("generator")
	case *ast.PropertyKeyed:
		if x.Kind == ast.PropertyKindGet || x.Kind == ast.PropertyKindSet {
			t.add("getter-setter")
		}
	case *ast.MethodDefinition:
		if x.Kind == ast.PropertyKindGet || x.Kind == ast.PropertyKindSet {
			t.add("getter-setter")
		}
	case *ast.LabelledStatement:
		t.add("label")
	case *ast.TemplateLiteral:
		if x.Tag != nil {
			if d, ok := x.Tag.(*ast.DotExpression); ok && d.Identifier.Name == "raw" {
				if id, ok := d.Left.(*ast.Identifier); ok && id.Name == "String" {
					t.add("String.raw")
					return true
				}
			}
			t.add("tagged-template")
		}
	case *ast.ForOfStatement:
		if x.Await {
			t.add("for-await")
		}
	case *ast.WithStatement:
		t.add("with")
	case *ast.MetaProperty:
		t.add("meta-property")
	case *ast.Identifier:
		if tag, ok := identTags[string(x.Name)]; ok {
			t.add(tag)
		}
	case *ast.NumberLiteral:
		if _, ok := x.Value.(*big.Int); ok {
			t.add("BigInt")
		}
	case *ast.RegExpLiteral:
		t.regex(x.Pattern, x.Flags)
	case *ast.NewExpression:
		if id, ok := x.Callee.(*ast.Identifier); ok && !builtinCtors[string(id.Name)] {
			t.add("new-user-fn")
		}
		if _, ok := x.Callee.(*ast.Identifier); !ok {
			t.add("new-user-fn")
		}
		t.regexpCall(x.Callee, x.ArgumentList)
	case *ast.CallExpression:
		t.regexpCall(x.Callee, x.ArgumentList)
		if id, ok := x.Callee.(*ast.Identifier); ok && id.Name == "import" {
			t.add("import")
		}
	case *ast.DotExpression:
		t.walk(reflect.ValueOf(x.Left))
		return false
	}
	return true
}

var astPkg = reflect.TypeOf(ast.Program{}).PkgPath()

func (t *tagger) walk(v reflect.Value) {
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			t.walk(v.Elem())
		}
	case reflect.Pointer:
		if v.IsNil() || v.Elem().Kind() != reflect.Struct || v.Elem().Type().PkgPath() != astPkg {
			return
		}
		if !t.node(v.Interface()) {
			return
		}
		t.walk(v.Elem())
	case reflect.Struct:
		if v.Type().PkgPath() != astPkg {
			return
		}
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).Name == "DeclarationList" {
				continue
			}
			f := v.Field(i)
			if f.Kind() == reflect.Struct && f.CanAddr() {
				t.walk(f.Addr())
				continue
			}
			t.walk(f)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			t.walk(v.Index(i))
		}
	}
}

var forAwaitRe = regexp.MustCompile(`\bfor\s+await\b`)

func Tags(code string) []string {
	t := &tagger{tags: map[string]bool{}}
	for _, cell := range harness.SplitCells(code) {
		prog, err := parser.ParseFile(nil, "cell.js", "(async function(){\n"+cell+"\n})", 0)
		if err != nil {
			t.add("goja-parse-error")
			if forAwaitRe.MatchString(cell) {
				t.add("for-await")
			}
			continue
		}
		t.walk(reflect.ValueOf(prog))
	}
	out := make([]string, 0, len(t.tags))
	for k := range t.tags {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func readIndex(path string) map[string]IndexEntry {
	m := map[string]IndexEntry{}
	f, err := os.Open(path)
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e IndexEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			m[e.ID] = e
		}
	}
	return m
}

func tagAll(args []string) error {
	feats := map[string][]string{}
	freq := map[string]map[string]int{}
	count := func(src string, tags []string) {
		if freq[src] == nil {
			freq[src] = map[string]int{}
		}
		freq[src]["(scripts)"]++
		if oracle.Tier1(tags) {
			freq[src]["(tier1)"]++
		}
		for _, tg := range tags {
			freq[src][tg]++
		}
	}
	dirs, _ := filepath.Glob("corpus/ext/*")
	held, _ := filepath.Glob("corpus/heldout/*")
	sort.Strings(dirs)
	sort.Strings(held)
	for _, d := range append(dirs, held...) {
		src := filepath.Base(d)
		if strings.Contains(d, "/heldout/") {
			src = "heldout/" + src
		}
		idx := readIndex(filepath.Join(d, "index.jsonl"))
		files, _ := filepath.Glob(filepath.Join(d, "*.js"))
		sort.Strings(files)
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			id := strings.TrimSuffix(filepath.Base(f), ".js")
			tags := Tags(string(b))
			if idx[id].Sloppy {
				tags = append(tags, "sloppy-only")
			}
			feats[src+"/"+id] = tags
			count(src, tags)
			count(src+":all", tags)
		}
		dropped, _ := filepath.Glob(filepath.Join(d, "dropped", "*.js"))
		for _, f := range dropped {
			b, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			count(src+":all", Tags(string(b)))
		}
	}
	for _, f := range harness.TranscriptFiles("corpus/replay/transcripts", nil) {
		t, err := harness.LoadTranscript(f)
		if err != nil {
			return err
		}
		for i, c := range t.Cells {
			tags := Tags(c)
			feats[fmt.Sprintf("replay/%s#%d", t.RID, i)] = tags
			count("replay", tags)
		}
	}
	b, _ := json.MarshalIndent(feats, "", " ")
	if err := os.WriteFile("corpus/features.json", append(b, '\n'), 0o644); err != nil {
		return err
	}
	table := freqTable(freq)
	fmt.Print(table)
	return os.WriteFile("corpus/features-freq.txt", []byte(table), 0o644)
}

func freqTable(freq map[string]map[string]int) string {
	srcs := make([]string, 0, len(freq))
	tagSet := map[string]bool{}
	for s, m := range freq {
		srcs = append(srcs, s)
		for t := range m {
			tagSet[t] = true
		}
	}
	sort.Strings(srcs)
	tags := make([]string, 0, len(tagSet))
	for t := range tagSet {
		tags = append(tags, t)
	}
	sort.Slice(tags, func(i, j int) bool {
		a, b := strings.HasPrefix(tags[i], "("), strings.HasPrefix(tags[j], "(")
		if a != b {
			return a
		}
		return tags[i] < tags[j]
	})
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-22s", "tag")
	for _, s := range srcs {
		fmt.Fprintf(&sb, " %8s", s)
	}
	sb.WriteString("\n")
	for _, t := range tags {
		fmt.Fprintf(&sb, "%-22s", t)
		for _, s := range srcs {
			fmt.Fprintf(&sb, " %8d", freq[s][t])
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
