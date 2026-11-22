package regex

import (
	"slices"
	"testing"
	"unicode/utf8"
)

const fuzzFlags = "gimsuy"

func flagsFromByte(b byte) string {
	var out []byte
	for i := 0; i < len(fuzzFlags); i++ {
		if b&(1<<i) != 0 {
			out = append(out, fuzzFlags[i])
		}
	}
	return string(out)
}

func safeExec(re *Regexp, s string, start int, bytes bool) (m []int, tooComplex bool) {
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(*Error)
			if !ok || e.Msg != errTooComplex {
				panic(r)
			}
			tooComplex = true
		}
	}()
	if bytes {
		return re.ExecBytes(s, start), false
	}
	return re.Exec(s, start), false
}

func FuzzRegex(f *testing.F) {
	for _, c := range loadCorpus(f) {
		if len(c.S) < 200 {
			f.Add(c.P, byte(0), c.S)
		}
	}
	f.Add(`(a+)+$`, byte(0), "aaaaaaaaaaaaaaaaaaaab")
	f.Add(`(?<=(\d+)(\d+))$`, byte(1), "1053")
	f.Add(`(?:(a)|b)\1*`, byte(2), "aAbB😀")
	old := SetStepLimit(200_000)
	defer SetStepLimit(old)
	f.Fuzz(func(t *testing.T, pattern string, fb byte, s string) {
		if len(pattern) > 64 || len(s) > 256 {
			return
		}
		flags := flagsFromByte(fb)
		bt, err := CompileEngine(pattern, flags, EngineBacktrack)
		r2, err2 := CompileEngine(pattern, flags, EngineRE2)
		if (err == nil) != (err2 == nil) {
			t.Fatalf("compile mismatch: %v vs %v", err, err2)
		}
		if err != nil {
			return
		}
		auto, err3 := CompileEngine(pattern, flags, EngineAuto)
		if err3 != nil {
			t.Fatalf("auto compile: %v", err3)
		}
		for _, start := range []int{0, 1, len(s) / 2, len(s)} {
			a, ac := safeExec(bt, s, start, false)
			safeExec(bt, s, start, true)
			if c, cc := safeExec(auto, s, start, false); !ac && !cc && utf8.ValidString(s) && !slices.Equal(a, c) {
				t.Fatalf("/%s/%s on %q from %d: backtrack %v, auto %v", pattern, flags, s, start, a, c)
			}
			if r2.re2 == nil || ac || !utf8.ValidString(s) {
				continue
			}
			b, bc := safeExec(r2, s, start, false)
			if !bc && !slices.Equal(a, b) {
				t.Fatalf("/%s/%s on %q from %d: backtrack %v, re2 %v (expr %s)", pattern, flags, s, start, a, b, r2.re2.expr)
			}
		}
	})
}
