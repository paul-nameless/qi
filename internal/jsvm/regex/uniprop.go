package regex

import (
	"errors"
	"sync"
	"unicode"
)

var errPropUnsupported = errors.New("unsupported property")

var gcLong = map[string]string{
	"Letter": "L", "Cased_Letter": "LC", "Uppercase_Letter": "Lu", "Lowercase_Letter": "Ll",
	"Titlecase_Letter": "Lt", "Modifier_Letter": "Lm", "Other_Letter": "Lo",
	"Mark": "M", "Combining_Mark": "M", "Nonspacing_Mark": "Mn", "Spacing_Mark": "Mc", "Enclosing_Mark": "Me",
	"Number": "N", "Decimal_Number": "Nd", "digit": "Nd", "Letter_Number": "Nl", "Other_Number": "No",
	"Punctuation": "P", "punct": "P", "Connector_Punctuation": "Pc", "Dash_Punctuation": "Pd",
	"Open_Punctuation": "Ps", "Close_Punctuation": "Pe", "Initial_Punctuation": "Pi",
	"Final_Punctuation": "Pf", "Other_Punctuation": "Po",
	"Symbol": "S", "Math_Symbol": "Sm", "Currency_Symbol": "Sc", "Modifier_Symbol": "Sk", "Other_Symbol": "So",
	"Separator": "Z", "Space_Separator": "Zs", "Line_Separator": "Zl", "Paragraph_Separator": "Zp",
	"Other": "C", "Control": "Cc", "cntrl": "Cc", "Format": "Cf", "Surrogate": "Cs",
	"Private_Use": "Co", "Unassigned": "Cn",
}

var scriptShort = map[string]string{
	"Latn": "Latin", "Cyrl": "Cyrillic", "Grek": "Greek", "Hani": "Han", "Arab": "Arabic",
	"Hebr": "Hebrew", "Hira": "Hiragana", "Kana": "Katakana", "Hang": "Hangul", "Thai": "Thai",
	"Deva": "Devanagari", "Zyyy": "Common", "Zinh": "Inherited", "Qaai": "Inherited",
	"Armn": "Armenian", "Geor": "Georgian", "Ethi": "Ethiopic", "Beng": "Bengali", "Taml": "Tamil",
	"Telu": "Telugu", "Knda": "Kannada", "Mlym": "Malayalam", "Gujr": "Gujarati", "Guru": "Gurmukhi",
	"Orya": "Oriya", "Sinh": "Sinhala", "Tibt": "Tibetan", "Mong": "Mongolian", "Khmr": "Khmer",
	"Laoo": "Lao", "Mymr": "Myanmar", "Copt": "Coptic", "Qaac": "Coptic", "Runr": "Runic",
	"Ogam": "Ogham", "Brai": "Braille", "Bopo": "Bopomofo", "Yiii": "Yi", "Cher": "Cherokee",
	"Syrc": "Syriac", "Thaa": "Thaana",
}

var unsupportedProps = map[string]bool{
	"Emoji": true, "Emoji_Presentation": true, "Emoji_Modifier": true, "Emoji_Modifier_Base": true,
	"Emoji_Component": true, "Extended_Pictographic": true, "EPres": true, "EMod": true, "EBase": true,
	"EComp": true, "ExtPict": true, "Bidi_Mirrored": true, "Bidi_M": true, "Case_Ignorable": true, "CI": true,
	"Changes_When_Casefolded": true, "Changes_When_Casemapped": true, "Changes_When_Lowercased": true,
	"Changes_When_NFKC_Casefolded": true, "Changes_When_Titlecased": true, "Changes_When_Uppercased": true,
	"CWCF": true, "CWCM": true, "CWKCF": true, "CWL": true, "CWT": true, "CWU": true,
	"Default_Ignorable_Code_Point": true, "DI": true, "Grapheme_Base": true, "Gr_Base": true,
	"XID_Continue": true, "XIDC": true, "XID_Start": true, "XIDS": true, "Cased": true,
	"Basic_Emoji": true, "RGI_Emoji": true,
}

var (
	propMu    sync.Mutex
	propCache = map[string][]rune{}
)

func propertyRanges(name, value string) ([]rune, error) {
	key := name + "=" + value
	propMu.Lock()
	rs, ok := propCache[key]
	propMu.Unlock()
	if ok {
		return rs, nil
	}
	rs, err := resolveProperty(name, value)
	if err != nil {
		return nil, err
	}
	rs = normRanges(rs)
	propMu.Lock()
	propCache[key] = rs
	propMu.Unlock()
	return rs, nil
}

func resolveProperty(name, value string) ([]rune, error) {
	if value != "" {
		switch name {
		case "General_Category", "gc":
			return categoryRanges(value)
		case "Script", "sc", "Script_Extensions", "scx":
			return scriptRanges(value)
		}
		return nil, errors.New("invalid")
	}
	if rs, err := categoryRanges(name); err == nil {
		return rs, nil
	}
	return binaryProperty(name)
}

func categoryRanges(v string) ([]rune, error) {
	if s, ok := gcLong[v]; ok {
		v = s
	}
	switch v {
	case "LC":
		return union("Lu", "Ll", "Lt"), nil
	case "Cn":
		return negateRanges(assigned(), maxCodePt), nil
	case "C":
		return append(union("Cc", "Cf", "Co", "Cs"), negateRanges(assigned(), maxCodePt)...), nil
	}
	if t, ok := unicode.Categories[v]; ok {
		return tableRanges(t), nil
	}
	return nil, errors.New("invalid")
}

func union(cats ...string) []rune {
	var out []rune
	for _, c := range cats {
		out = append(out, tableRanges(unicode.Categories[c])...)
	}
	return out
}

func assigned() []rune {
	var out []rune
	for k, t := range unicode.Categories {
		if len(k) == 2 && k != "Cn" && k != "LC" {
			out = append(out, tableRanges(t)...)
		}
	}
	return normRanges(out)
}

func scriptRanges(v string) ([]rune, error) {
	if s, ok := scriptShort[v]; ok {
		v = s
	}
	if t, ok := unicode.Scripts[v]; ok {
		return tableRanges(t), nil
	}
	return nil, errors.New("invalid")
}

func binaryProperty(name string) ([]rune, error) {
	switch name {
	case "Any":
		return []rune{0, maxCodePt}, nil
	case "ASCII":
		return []rune{0, 0x7F}, nil
	case "Assigned":
		return assigned(), nil
	case "Alphabetic", "Alpha":
		return append(union("Lu", "Ll", "Lt", "Lm", "Lo", "Nl"), tableRanges(unicode.Other_Alphabetic)...), nil
	case "Uppercase", "Upper":
		return append(union("Lu"), tableRanges(unicode.Other_Uppercase)...), nil
	case "Lowercase", "Lower":
		return append(union("Ll"), tableRanges(unicode.Other_Lowercase)...), nil
	case "Math":
		return append(union("Sm"), tableRanges(unicode.Other_Math)...), nil
	case "White_Space", "space":
		return tableRanges(unicode.White_Space), nil
	case "Grapheme_Extend", "Gr_Ext":
		return append(union("Mn", "Me"), tableRanges(unicode.Other_Grapheme_Extend)...), nil
	case "ID_Start", "IDS":
		return idStart(), nil
	case "ID_Continue", "IDC":
		return idContinue(), nil
	}
	if unsupportedProps[name] {
		return nil, errPropUnsupported
	}
	if t, ok := unicode.Properties[name]; ok {
		return tableRanges(t), nil
	}
	return nil, errors.New("invalid")
}

func idStart() []rune {
	rs := append(union("Lu", "Ll", "Lt", "Lm", "Lo", "Nl"), tableRanges(unicode.Other_ID_Start)...)
	return subtract(rs, append(tableRanges(unicode.Pattern_Syntax), tableRanges(unicode.Pattern_White_Space)...))
}

func idContinue() []rune {
	rs := append(idStart(), union("Mn", "Mc", "Nd", "Pc")...)
	rs = append(rs, tableRanges(unicode.Other_ID_Continue)...)
	return subtract(rs, append(tableRanges(unicode.Pattern_Syntax), tableRanges(unicode.Pattern_White_Space)...))
}

func subtract(a, b []rune) []rune {
	return negateRanges(append(negateRanges(a, maxCodePt), b...), maxCodePt)
}
