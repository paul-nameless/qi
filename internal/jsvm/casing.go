package jsvm

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

var upperSpecial = map[rune]string{
	0x00DF: "SS", 0x0149: "ʼN", 0x01F0: "J̌", 0x0390: "Ϊ́", 0x03B0: "Ϋ́",
	0x0587: "ԵՒ", 0x1E96: "H̱", 0x1E97: "T̈", 0x1E98: "W̊", 0x1E99: "Y̊", 0x1E9A: "Aʾ",
	0x1F50: "Υ̓", 0x1F52: "Υ̓̀", 0x1F54: "Υ̓́", 0x1F56: "Υ̓͂",
	0x1FB6: "Α͂", 0x1FC6: "Η͂", 0x1FD2: "Ϊ̀", 0x1FD3: "Ϊ́",
	0x1FD6: "Ι͂", 0x1FD7: "Ϊ͂", 0x1FE2: "Ϋ̀", 0x1FE3: "Ϋ́",
	0x1FE4: "Ρ̓", 0x1FE6: "Υ͂", 0x1FE7: "Ϋ͂", 0x1FF6: "Ω͂",
	0x1FB3: "ΑΙ", 0x1FBC: "ΑΙ", 0x1FC3: "ΗΙ", 0x1FCC: "ΗΙ", 0x1FF3: "ΩΙ",
	0x1FFC: "ΩΙ", 0x1FB2: "ᾺΙ", 0x1FB4: "ΆΙ", 0x1FC2: "ῊΙ", 0x1FC4: "ΉΙ",
	0x1FF2: "ῺΙ", 0x1FF4: "ΏΙ", 0x1FB7: "Α͂Ι", 0x1FC7: "Η͂Ι",
	0x1FF7: "Ω͂Ι", 0xFB00: "FF", 0xFB01: "FI", 0xFB02: "FL", 0xFB03: "FFI", 0xFB04: "FFL", 0xFB05: "ST",
	0xFB06: "ST", 0xFB13: "ՄՆ", 0xFB14: "ՄԵ", 0xFB15: "ՄԻ", 0xFB16: "ՎՆ",
	0xFB17: "ՄԽ",
}

func init() {
	for i := rune(0); i < 8; i++ {
		upperSpecial[0x1F80+i] = string(0x1F08+i) + "Ι"
		upperSpecial[0x1F88+i] = string(0x1F08+i) + "Ι"
		upperSpecial[0x1F90+i] = string(0x1F28+i) + "Ι"
		upperSpecial[0x1F98+i] = string(0x1F28+i) + "Ι"
		upperSpecial[0x1FA0+i] = string(0x1F68+i) + "Ι"
		upperSpecial[0x1FA8+i] = string(0x1F68+i) + "Ι"
	}
}

func jsToUpper(s string) string {
	if isASCIIString(s) {
		return strings.ToUpper(s)
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if sp, ok := upperSpecial[r]; ok {
			b.WriteString(sp)
			continue
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

func jsToLower(s string) string {
	if isASCIIString(s) {
		return strings.ToLower(s)
	}
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range s {
		switch r {
		case 0x0130:
			b.WriteString("i̇")
		case 0x03A3:
			if finalSigma(s, i) {
				b.WriteRune(0x03C2)
			} else {
				b.WriteRune(0x03C3)
			}
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func caseIgnorable(r rune) bool {
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk) || r == '\'' || r == '.' || r == ':' || r == 0x00B7 || r == 0x2019
}

func cased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r)
}

func finalSigma(s string, i int) bool {
	before := false
	for j := i; j > 0; {
		r, size := utf8.DecodeLastRuneInString(s[:j])
		j -= size
		if caseIgnorable(r) {
			continue
		}
		before = cased(r)
		break
	}
	if !before {
		return false
	}
	for j := i + utf8.RuneLen(0x03A3); j < len(s); {
		r, size := utf8.DecodeRuneInString(s[j:])
		j += size
		if caseIgnorable(r) {
			continue
		}
		return !cased(r)
	}
	return true
}
