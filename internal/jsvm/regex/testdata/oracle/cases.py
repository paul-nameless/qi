import json, sys

GO_SRC = '''package main

import (
	"fmt"
	"strings"
)

// Server handles requests.
type Server struct {
	addr string
	port int
}

func (s *Server) Start(ctx context.Context) error {
	return nil
}

func NewServer(addr string, port int) *Server {
	// TODO: validate port
	return &Server{addr: addr, port: port}
}

func helper() {}
	func  indented_func ( x int ) {}
'''

PY_SRC = '''import os
from typing import List, Optional

class Parser(Base):
    def __init__(self, text: str) -> None:
        self.text = text  # FIXME: copy?

    async def parse(self, *args, **kw) -> List[str]:
        return [x for x in self.text.split()]

def main():
    print("hello, world")
'''

JS_SRC = '''import { readFile } from "node:fs/promises";
import path from 'path';
const re = /ab+c/gi;
export async function load(name) {
  const data = await readFile(path.join(dir, name), "utf8");
  let count = 0; // counter
  return data.split("\\n").filter((l) => l.trim() !== "");
}
export const VERSION = "1.2.3";
class Foo extends Bar { #priv = 1; get x() { return this.#priv; } }
'''

LOG = '''2024-01-15T10:23:45.123Z INFO  [main] server started on :8080
2024-01-15T10:23:46.001Z WARN  [db] slow query took 1532ms
2024-01-15T10:24:01.777Z ERROR [api] request failed: status=500 path=/v1/users/42
2024-01-15 10:25:00 DEBUG cache hit ratio 0.93
127.0.0.1 - - [15/Jan/2024:10:26:00 +0000] "GET /index.html HTTP/1.1" 200 1043
192.168.1.254 - bob [15/Jan/2024:10:26:01 +0000] "POST /api/login HTTP/2" 401 12
'''

CSV = '''name,age,city
"Smith, John",42,"New York"
Jane,37,Paris
"O""Brien",,Dublin
'''

MD = '''# Title

Some *emphasis* and **bold** text with `code` and a [link](https://example.com/a?b=1&c=2).

## Section 2
- item one
- item two
  - nested

```go
fmt.Println("x")
```
'''

UNI = 'Ünïcödé café naïve résumé Привет мир Ελληνικά 中文字符 日本語 한국어 😀 emoji 👍🏽 done'
ASTRAL = 'a😀b𝒳c𐐷d🎉'
TEXT = 'The quick brown fox jumps over the lazy dog. THE QUICK BROWN FOX! 123 4567 89.'

cases = []

def add(p, f, *subjects):
    for s in subjects:
        cases.append([p, f, s])

# basics
add(r'abc', '', 'abc', 'xxabcxxabc', 'ab', '')
add(r'a|b|c', 'g', 'xaybzc')
add(r'foo|foobar', '', 'foobar')
add(r'foobar|foo', '', 'foobar')
add(r'(a|ab)(c|bcd)(d*)', '', 'abcd')
add(r'', 'g', 'abc')
add(r'(?:)', 'g', 'ab')
add(r'a(?:b|c)d', 'g', 'abd acd aed')
add(r'(a)(b)(c)(d)(e)(f)(g)(h)(i)(j)(k)', '', 'abcdefghijk')
add(r'x*', 'g', 'axxb')
add(r'x*?', 'g', 'axxb')
add(r'a+', 'g', 'caaandaa')
add(r'a+?', 'g', 'caaand')
add(r'a??b', 'g', 'ab b aab')
add(r'a{2}', 'g', 'aaaaa')
add(r'a{2,}', 'g', 'a aa aaaaa')
add(r'a{2,3}', 'g', 'aaaaaaa')
add(r'a{2,3}?', 'g', 'aaaaaaa')
add(r'a{0}b', 'g', 'ab b')
add(r'(ab){1,2}', 'g', 'abababab')
add(r'(?:ab){2,3}?c', 'g', 'abababc ababc abc')
add(r'(a|b)*c', '', 'abababbc')
add(r'(a|b)*?c', '', 'abababbc')
add(r'(.*)(\d+)', '', 'abc123')
add(r'(.*?)(\d+)', '', 'abc123')
add(r'(.+)(.+)', '', 'abcd')
add(r'^(a+)*$', '', 'aaaa', 'aaab')
add(r'(x+x+)+y', '', 'xxxxxxxxxxy')
add(r'((a)|b)+', '', 'ab', 'ba')
add(r'(?:(a)|b)+', '', 'ab', 'aab')
add(r'(?:(a)|(b))+', '', 'ab', 'ba')
add(r'(a*)*', '', 'b', 'aab')
add(r'(a*)+', '', 'b', 'aab')
add(r'(a*?)*', '', 'aa')
add(r'(?:a*)*b', 'g', 'aab b')
add(r'(?:|a)*', '', 'aa')
add(r'(?:a|)*', '', 'aa')
add(r'(a?)*?b', '', 'aab')
add(r'(?:a?b?)*', 'g', 'abba c')
add(r'(z)((a+)?(b+)?(c))*', '', 'zaacbbbcac')
add(r'(a)|b', 'g', 'ab')
add(r'(?:(\d+)-)?(\d+)', 'g', '10-20 30')
add(r'(\w+)\s(\w+)', 'g', 'John Smith, Jane Doe')
add(r'(a(b(c)))', '', 'abc')
add(r'((((((((((a))))))))))\10', '', 'aa')
# classes and escapes
add(r'\d+', 'g', 'a1b22c333', UNI, '٣٤ 12')
add(r'\D+', 'g', 'a1b22c333')
add(r'\w+', 'g', TEXT, UNI)
add(r'\W+', 'g', TEXT, 'a-b_c')
add(r'\s+', 'g', 'a b\tc\nd e f﻿g　h\u0085i᠎j')
add(r'\S+', 'g', 'a b\tc d')
add(r'[abc]+', 'g', 'xxabcaxbb')
add(r'[^abc]+', 'g', 'xxabcaxbb')
add(r'[a-z]+', 'g', TEXT)
add(r'[A-Z][a-z]*', 'g', TEXT)
add(r'[0-9a-fA-F]+', 'g', 'deadBEEF 0x1f zz')
add(r'[\d\s]+', 'g', 'ab 12 3\t4x')
add(r'[^\d\s]+', 'g', 'ab 12 3\t4x')
add(r'[\w-]+', 'g', 'foo-bar baz_qux')
add(r'[a\-z]+', 'g', 'a-z b')
add(r'[-a]+', 'g', '-a-b')
add(r'[a-]+', 'g', '-a-b')
add(r'[\]]+', 'g', 'a]]b')
add(r'[]', 'g', 'abc')
add(r'[^]', 'g', 'a\nb')
add(r'[^]*', '', 'ab\ncd')
add(r'a[]b', '', 'ab')
add(r'[\b]', 'g', 'a\bb')
add(r'\bfoo\b', 'g', 'foo foobar barfoo foo.')
add(r'\Boo\B', 'g', 'foo boot oo')
add(r'\b', 'g', 'ab cd')
add(r'\B', 'g', 'ab cd')
add(r'\b\w', 'g', 'héllo wörld')
add(r'[\s\S]+', '', 'a\nb')
add(r'\t\n\v\f\r', '', '\t\n\v\f\r')
add(r'\x41B\103', '', 'ABC')
add(r'\0', '', 'a\u0000b')
add(r'[\x00-\x1f]+', 'g', 'a\u0001\u0002b')
add(r'\cJ', '', 'a\nb')
add(r'\cj', '', 'a\nb')
add(r'[\cJ]', '', 'a\nb')
add(r'\/', 'g', 'a/b/c')
add(r'[/]', 'g', 'a/b')
add(r'\.', 'g', 'a.b.c')
add(r'\$\d+(?:\.\d\d)?', 'g', 'cost $5 or $12.50 or $.5')
add(r'\^', 'g', 'a^b')
add(r'[.*+?^${}()|[\]\\]', 'g', 'a.b*c+d?e^f$g{h}i(j)k|l[m]n\\o')
add(r'é', 'g', 'café é')
add(r'[à-ÿ]+', 'g', UNI)
add(r'[а-яё]+', 'gi', UNI + ' ЁЖ')
add(r'[一-鿿]+', 'g', UNI)
# anchors and multiline
add(r'^abc', '', 'abc', 'xabc')
add(r'^abc', 'g', 'abc\nabc')
add(r'^abc', 'gm', 'abc\nabc\rabc abc abc')
add(r'abc$', 'g', 'abc\nabc')
add(r'abc$', 'gm', 'abc\nabc\r\nabc')
add(r'$', 'g', 'ab\n')
add(r'^', 'gm', 'a\nb\n')
add(r'$', 'gm', 'a\nb\r\nc')
add(r'^$', 'gm', 'a\n\nb\n')
add(r'^.*$', 'gm', 'line1\nline2\r\nline3\n')
add(r'^\s*$', 'gm', 'a\n  \n\t\nb')
add(r'^(\w+):\s*(.*)$', 'gm', 'Host: example.com\nAccept: */*\r\nX-Empty:\n')
add(r'^#+\s+(.+)$', 'gm', MD)
add(r'^\s*[-*]\s+(.*)$', 'gm', MD)
add(r'^```(\w*)$', 'gm', MD)
add(r'(?:^|,)("(?:[^"]|"")*"|[^,\n]*)', 'gm', CSV)
add(r'^(?:"([^"]*(?:""[^"]*)*)"|([^,]*))(?:,|$)', 'gm', CSV)
add(r'^', 'g', 'abc')
add(r'a^b', '', 'a^b')
add(r'x$|y', 'g', 'xyx')
# dot
add(r'.', 'g', 'a\nb\rc d e')
add(r'.', 'gs', 'a\nb\rc')
add(r'a.c', 'g', 'abc a\nc a.c')
add(r'a.c', 'gs', 'abc a\nc')
add(r'.+', 'g', 'line1\nline2')
add(r'.+', 'gs', 'line1\nline2')
add(r'^.$', '', '😀')
add(r'^.$', 'u', '😀')
add(r'^..$', '', '😀')
add(r'.', 'g', ASTRAL)
add(r'.', 'gu', ASTRAL)
add(r'[^a]', 'g', ASTRAL)
add(r'[^a]', 'gu', ASTRAL)
add(r'\S', 'g', '😀x')
add(r'\S', 'gu', '😀x')
add(r'😀', 'g', ASTRAL)
add(r'😀+', '', '😀😀')
add(r'😀+', 'u', '😀😀')
add(r'[😀]', 'g', '😀')
add(r'[😀]', 'gu', '😀')
add(r'\ud83d', 'g', '😀')
add(r'😀', 'g', 'x😀')
add(r'😀', 'gu', 'x😀')
add(r'\u{1F600}', 'gu', 'x😀')
add(r'[\u{1F600}-\u{1F64F}]+', 'gu', 'hi 😀😃🙏 x')
add(r'\u{61}', 'u', 'a')
add(r'\u{61}', '', 'uuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuu', 'a')
add(r'(?:)', 'gu', '😀a')
add(r'', 'g', '😀')
add(r'x*', 'gu', 'a😀x')
add(r'\b', 'g', 'é😀a')
# case-insensitive
add(r'abc', 'gi', 'ABC aBc abc xbc')
add(r'[a-z]+', 'gi', TEXT)
add(r'[^a-z]+', 'gi', TEXT)
add(r'\w+', 'gi', 'Straße ſ K k')
add(r'\w', 'giu', 'ſ K k s')
add(r'\W', 'giu', 'ſ K k s !')
add(r'\W', 'gi', 'ſ K k s !')
add(r's', 'gi', 'sSſ')
add(r's', 'giu', 'sSſ')
add(r'k', 'gi', 'kKK')
add(r'k', 'giu', 'kKK')
add(r'[k-l]', 'giu', 'kKKlL')
add(r'[^k]', 'giu', 'kKKa')
add(r'ß', 'gi', 'ßẞss')
add(r'ß', 'giu', 'ßẞss')
add(r'σ', 'gi', 'ΣσςΣ')
add(r'Σ', 'giu', 'σςΣ')
add(r'é', 'gi', 'éÉe')
add(r'[à-þ]+', 'gi', 'ÀÉÎõü')
add(r'привет', 'gi', 'ПРИВЕТ Привет')
add(r'i', 'gi', 'iIİı')
add(r'i', 'giu', 'iIİı')
add(r'µ', 'gi', 'µΜμ')
add(r'K', 'gi', 'kKK')
add(r'K', 'giu', 'kKK')
add(r'(a)\1', 'gi', 'aA Aa aa')
add(r'(é)\1', 'gi', 'éÉ')
add(r'(ſ)\1', 'giu', 'ſs ſS')
add(r'(ſ)\1', 'gi', 'ſs ſſ')
add(r'\p{Lu}', 'giu', 'aB')
add(r'\P{Lu}', 'giu', 'aB1')
add(r'[ΐ]', 'giu', 'ΐΐ')
add(r'ᾀ', 'gi', 'ᾀᾈ')
add(r'ǅ', 'gi', 'Ǆǅǆ')
add(r'(?:foo|BAR)+', 'gi', 'FOObarFooBar baz')
add(r'\bthe\b', 'gi', TEXT)
# unicode properties
add(r'\p{L}+', 'gu', UNI)
add(r'\p{Lu}', 'gu', 'aBcDÉ')
add(r'\p{Ll}+', 'gu', 'aBcdÉé')
add(r'\p{N}+', 'gu', 'a12½٣')
add(r'\p{Nd}+', 'gu', 'a12½٣')
add(r'\p{P}+', 'gu', 'a,b.c!?¿«»')
add(r'\p{S}+', 'gu', 'a+b$c€©😀')
add(r'\p{Z}+', 'gu', 'a b c d')
add(r'\p{Script=Latin}+', 'gu', UNI)
add(r'\p{Script=Cyrillic}+', 'gu', UNI)
add(r'\p{sc=Greek}+', 'gu', UNI)
add(r'\p{Script=Han}+', 'gu', UNI)
add(r'\p{Script=Hiragana}+', 'gu', 'ひらがなカタカナ')
add(r'\p{General_Category=Letter}+', 'gu', 'ab1c')
add(r'\p{Letter}+', 'gu', 'ab1c')
add(r'\P{L}+', 'gu', UNI)
add(r'[\p{L}\p{N}_]+', 'gu', UNI)
add(r'[^\p{L}\s]+', 'gu', UNI)
add(r'\p{Alphabetic}+', 'gu', 'abc١٢ⅫX')
add(r'\p{White_Space}+', 'gu', 'a \u0085b')
add(r'\p{ASCII}+', 'gu', 'abcé')
add(r'\p{Any}', 'gu', '😀a')
add(r'\p{Uppercase}+', 'gu', 'ABcⅫ')
add(r'\p{L}', 'g', 'p{L}')
add(r'\p{Script_Extensions=Latin}+', 'gu', 'abc Δ')
# sticky
add(r'a', 'y', 'aab', 'baa')
add(r'a', 'gy', 'aaba')
add(r'\d+', 'y', '12 34')
add(r'\d+', 'gy', '1234 56')
add(r'^a', 'y', 'aa')
add(r'^a', 'my', 'a\na')
add(r'(?:a|b)', 'y', 'abcab')
add(r'b', 'y', 'ab')
add(r'', 'y', 'ab')
add(r'\w+\s*', 'y', 'foo bar  baz!qux')
add(r'(?<=a)b', 'y', 'abab')
add(r'abc', 'y', 'abcabcx')
add(r'abc', 'iy', 'ABCabc')
# lookahead / lookbehind
add(r'o(?=r)', 'g', 'lorem ipsum dolor ' * 3)
add(r'o(?!r)', 'g', 'lorem ipsum dolor')
add(r'\d+(?=px)', 'g', '10px 20em 30px')
add(r'\d+(?!px|\d)', 'g', '10px 20em 30px')
add(r'(?=(\w+))\w', 'g', 'abc de')
add(r'(?=(a+))a*b\1', '', 'baaabac')
add(r'(?=(a+?))(\1ab)', '', 'aaab')
add(r'(?!(a))\1b', 'g', 'ab b')
add(r'(.*?)a(?!(a+)b\2c)\2(.*)', '', 'baaabaac')
add(r'(?<=\$)\d+(\.\d*)?', 'g', 'cost $10.50, €20, $3')
add(r'(?<!\$)\b\d+', 'g', 'cost $10 20 $30 40')
add(r'(?<=(\d+)(\d+))$', '', '1053')
add(r'(?<=(\d+?)(\d+?))$', '', '1053')
add(r'(?<=\1(a))b', '', 'aab')
add(r'(?<=(a)\1)b', '', 'aab')
add(r'(?<=(?<a>\w){3})f', '', 'abcdef')
add(r'(?<=a(?=b)b)c', 'g', 'abc ac')
add(r'(?<=^|,)\w+', 'g', 'a,bb,ccc')
add(r'(?<!^)\b\w', 'g', 'one two three')
add(r'(?<=\b)\w', 'g', 'ab cd')
add(r'(?<=[a-z]{2})\d', 'g', 'ab1 a2 xyz3')
add(r'(?<=a.*?)b', 'g', 'xbab')
add(r'(?<=é)\w', 'g', 'éa éé')
add(r'(?<=😀)a', 'g', '😀a')
add(r'(?<=😀)a', 'gu', '😀a')
add(r'(?<=.)a', 'gu', '😀a')
add(r'(?<=.)a', 'g', '😀a')
add(r'(?<=(?:ab)+)c', 'g', 'ababc')
add(r'(?<=a|bc)d', 'g', 'ad bcd cd')
add(r'(?<!a|b)c', 'g', 'ac bc cc')
add(r'(?=a)*', '', 'a')
add(r'(?=a)+a', '', 'a')
add(r'(?=a){2}', 'g', 'ab')
add(r'(?!a)+b', '', 'b')
add(r'(?=.*\d)(?=.*[a-z])(?=.*[A-Z]).{8,}', '', 'Passw0rdX', 'password1')
add(r'^(?!.*\.\.)[\w.]+@\w+\.\w+$', '', 'a.b@c.de', 'a..b@c.de')
add(r'\b(?!(?:if|for|while)\b)\w+(?=\s*\()', 'g', 'if (x) foo(1); for(;;) bar (2)')
add(r'(?<=<(\w+)>).*?(?=<\/\1>)', 'g', '<b>bold</b> <i>it</i>')
add(r'(?<=(?<!a)b)c', 'g', 'bc abc')
add(r'(?<=a(?:b|(c)))d', 'g', 'abd acd')
add(r'(?<!\\)"', 'g', 'a"b\\"c"')
add(r'(?<=\s|^)#\w+', 'g', '#tag mid#no #yes')
# backreferences
add(r'(\w)\1', 'g', 'aabbcdd')
add(r'(\w+)\s+\1', 'g', 'the the cat cat sat')
add(r'\b(\w+)\s+\1\b', 'gi', 'The the cat Cat sat')
add(r'(["\'])(.*?)\1', 'g', '''say "hi" and 'bye' and "it's"''')
add(r'<(\w+)[^>]*>(.*?)<\/\1>', 'g', '<b>x</b><i class="a">y</i><b>z</i>')
add(r'(?<q>["\']).*?\k<q>', 'g', '''a "b" 'c' "d'e"''')
add(r'(?<year>\d{4})-(?<month>\d{2})-(?<day>\d{2})', 'g', '2024-01-15 and 1999-12-31')
add(r'\k<a>(?<a>x)', '', 'xx')
add(r'\1(a)', '', 'aa')
add(r'(a\1)', '', 'aa')
add(r'(a)|\1b', 'g', 'b ab')
add(r'(?:(a)|b)\1', 'g', 'aa b bb')
add(r'(a)?\1b', 'g', 'aab b')
add(r'(\d)(\d)\2\1', 'g', '1221 1234 3443')
add(r'(.)(?!\1).', 'g', 'aabbab')
add(r'\2(a)(b)', '', 'ab')
add(r'(?<n>a)\k<n>\1', '', 'aaa')
add(r'(a)(?:\1){2,}', 'g', 'aaaa aa')
add(r'(ab|a)\1+', '', 'ababab aaa')
add(r'(.)\1{2}', 'g', 'aaabbbbcc')
add(r'^(a+)\1*$', '', 'aaaa', 'aaa')
# annex B and escapes
add(r'\1', '', '\u0001')
add(r'\12', '', '\n', '\u00012')
add(r'(a)\12', '', 'aa2', 'a\n')
add(r'\8', '', '8')
add(r'\9', '', '9')
add(r'\08', '', '\u00008')
add(r'\377', '', 'ÿ')
add(r'\400', '', ' 0')
add(r'a{', '', 'a{')
add(r'a{1', '', 'a{1')
add(r'a{1,', '', 'a{1,')
add(r'a{,2}', '', 'a{,2}')
add(r'{', '', '{')
add(r'}', '', '}')
add(r']', '', ']')
add(r'x{1}{', '', 'x{')
add(r'\c', '', '\\c')
add(r'\c1', '', '\\c1')
add(r'[\c1]', '', '\u0011')
add(r'[\c_]', '', '\u001f')
add(r'[\c*]', 'g', '\\c*')
add(r'\a\e\g\j', '', 'aegj')
add(r'\xZ', '', 'xZ')
add(r'\u12', '', 'u12')
add(r'\k', '', 'k')
add(r'\k<a>', '', 'k<a>')
add(r'[\d-z]+', 'g', '1-z a')
add(r'[z-\d]+', 'g', '1-z a')
add(r'[\w-.]+', 'g', 'a.b-c d')
add(r'\p', '', 'p')
add(r'\P{L}', '', 'P{L}')
add(r'[\B]', '', 'B')
add(r'\-', '', '-')
add(r'(?=a){0}', '', 'b')
add(r'a{0,0}', 'g', 'aa')
add(r'a{1,1}?', 'g', 'aa')
add(r'a{2147483648}', '', 'a')
add(r'x{3,4294967296}', '', 'xxx')
# errors
for p, f in [('(', ''), (')', ''), ('[', ''), ('a**', ''), ('*a', ''), ('a{2,1}', ''), ('(?<n>a)(?<n>b)', ''),
             ('\\k<x>(?<a>.)', ''), ('(?<1a>x)', ''), ('[b-a]', ''), ('\\', ''), ('a', 'gg'), ('a', 'x'),
             ('(?<=a)+', ''), ('(?<!a)*', ''), ('\\u{110000}', 'u'), ('\\p{Foo}', 'u'), ('{', 'u'), (']', 'u'),
             ('\\a', 'u'), ('\\1', 'u'), ('(?=a)*', 'u'), ('[\\d-z]', 'u'), ('\\c', 'u'), ('a{', 'u'), ('(?a)', ''),
             ('\\00', 'u'), ('x{2,1}?', ''), ('^*', ''), ('\\b+', ''), ('(?:', ''), ('a|*', ''), ('[a-\\w]', 'u'),
             ('\\k<a', ''), ('(?<a>.)\\k<b>', ''), ('\\u{', 'u'), ('\\x1', 'u'), ('[', 'u'), ('a', 'uv')]:
    add(p, f, 'a')
# coding-agent patterns
add(r'^\s*func\s+(?:\([^\n)]*\)\s*)?([A-Za-z_]\w*)\s*\(', 'gm', GO_SRC)
add(r'^func\s+(\w+)', 'gm', GO_SRC)
add(r'func \((\w+) \*?(\w+)\) (\w+)\(', 'g', GO_SRC)
add(r'^type\s+(\w+)\s+(struct|interface)\b', 'gm', GO_SRC)
add(r'^import \(\n((?:\t"[^"]+"\n)+)\)', 'm', GO_SRC)
add(r'"([^"\\]|\\.)*"', 'g', GO_SRC + ' "esc\\"aped"')
add(r'//\s*(TODO|FIXME|XXX):?\s*(.*)', 'g', GO_SRC + PY_SRC)
add(r'#\s*(TODO|FIXME)\b:?\s*(.*)$', 'gm', PY_SRC)
add(r'^\s*(?:async\s+)?def\s+(\w+)\s*\(([^)]*)\)', 'gm', PY_SRC)
add(r'^class\s+(\w+)(?:\(([^)]*)\))?:', 'gm', PY_SRC)
add(r'^(?:from\s+([\w.]+)\s+)?import\s+(.+)$', 'gm', PY_SRC)
add(r'^import\s+(?:\{([^}]*)\}|(\w+))\s+from\s+["\']([^"\']+)["\'];?', 'gm', JS_SRC)
add(r'\bexport\s+(?:async\s+)?(?:function|const|class|let)\s+(\w+)', 'g', JS_SRC)
add(r'(?:const|let|var)\s+(\w+)\s*=', 'g', JS_SRC)
add(r'\/\/.*$', 'gm', JS_SRC)
add(r'#\w+', 'g', JS_SRC)
add(r'\b\d+\.\d+\.\d+\b', 'g', JS_SRC + ' v10.20.30 1.2')
add(r'^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$', '', '1.2.3', 'v1.0.0-alpha.1+build.5', '1.02.3', '1.2.3-rc.1')
add(r'^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2}):(\d{2})(?:\.(\d+))?Z?\s+(\w+)\s+(.*)$', 'gm', LOG)
add(r'\b(ERROR|WARN)\b.*', 'g', LOG)
add(r'status=(\d{3})', 'g', LOG)
add(r'took (\d+)ms', 'g', LOG)
add(r'^(\S+) \S+ (\S+) \[([^\]]+)\] "(\w+) (\S+) ([^"]+)" (\d{3}) (\d+)$', 'gm', LOG)
add(r'\b(?:\d{1,3}\.){3}\d{1,3}\b', 'g', LOG)
add(r'\b(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)\b', 'g', LOG + ' 999.1.1.1 10.0.0.256')
add(r'https?:\/\/[^\s)"\']+', 'g', MD + ' http://x.y/z?q=1#f, https://a.b')
add(r'^(https?):\/\/([^\/:?#]+)(?::(\d+))?(\/[^?#]*)?(?:\?([^#]*))?(?:#(.*))?$', '', 'https://example.com:8443/a/b?x=1&y=2#frag', 'http://localhost', 'ftp://x')
add(r'\[([^\]]+)\]\(([^)]+)\)', 'g', MD)
add(r'\*\*(.+?)\*\*', 'g', MD)
add(r'(?<!\*)\*([^*]+)\*(?!\*)', 'g', MD)
add(r'`([^`]+)`', 'g', MD)
add(r'```(\w+)?\n([\s\S]*?)```', 'g', MD)
add(r'[\w.+-]+@[\w-]+\.[\w.-]+', 'g', 'mail a.b+c@ex-ample.co.uk, x@y.z and @no')
add(r'^[^,\n]*(?:,[^,\n]*){2}$', 'gm', CSV)
add(r',(?=(?:[^"]*"[^"]*")*[^"]*$)', 'g', '"Smith, John",42,"New, York",x')
add(r'"((?:[^"]|"")*)"', 'g', CSV)
add(r'\s+$', 'gm', 'a  \nb\t\nc')
add(r'^\s+|\s+$', 'g', '  trim me  ')
add(r'(\r\n|\r|\n)', 'g', 'a\r\nb\rc\nd')
add(r'[A-Z]', 'g', 'camelCaseString')
add(r'([a-z])([A-Z])', 'g', 'camelCaseStringX')
add(r'(?:^|_)([a-z])', 'g', 'snake_case_name')
add(r'[-_\s]+(.)?', 'g', 'foo-bar_baz qux')
add(r'\$\{(\w+)\}', 'g', 'path ${HOME}/x ${USER}')
add(r'\{\{\s*([\w.]+)\s*\}\}', 'g', 'hi {{ name }} and {{user.id}}')
add(r'%(\d+\$)?[-+ 0#]*\d*(?:\.\d+)?[sdfxX%]', 'g', 'val=%d %5.2f %-10s %1$s %%')
add(r'^\s*"(\w+)":\s*("(?:[^"\\]|\\.)*"|-?\d+(?:\.\d+)?|true|false|null)', 'gm', '{\n  "a": 1,\n  "b": "x\\"y",\n  "c": true,\n  "d": -2.5\n}')
add(r'\b[0-9a-f]{7,40}\b', 'g', 'commit 12da9a8 and 5fb658e1234abcd; not zzzzzzz')
add(r'^([ MADRCU?!]{2}) (.+)$', 'gm', ' M file.go\n?? new.txt\nA  added.go\n')
add(r'^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@', 'gm', 'diff\n@@ -1,3 +1,4 @@\n x\n@@ -10 +11,2 @@ func\n')
add(r'^([+-])(?![+-]{2})(.*)$', 'gm', '--- a/x\n+++ b/x\n-old\n+new\n same\n')
add(r'(\w+)=("[^"]*"|\S*)', 'g', 'a=1 b="two words" c= d=x')
add(r'^(\w+)\s*=\s*(.*?)\s*$', 'gm', 'key = value  \nother=  x\n')
add(r'([^/]+)$', '', '/usr/local/bin/qi')
add(r'^(.*\/)?([^/]*?)(\.[^./]*)?$', '', '/a/b/file.tar.gz', 'noext', '.bashrc')
add(r'\.(go|py|js|ts)$', '', 'main.go', 'x.rs')
add(r'(\d+)(?:\.(\d+))?', 'g', 'v 3.14 and 42 and .5')
add(r'-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?', 'g', 'x=-1.5e10 y=3 z=2E-3')
add(r'0x[0-9a-fA-F]+|\d+', 'g', '0x1F 255 0xg')
add(r'\b[A-Z_][A-Z0-9_]{2,}\b', 'g', 'MAX_SIZE = 10; HTTP2 ok; Ab; A_B')
add(r'(?:\\.|[^\\"])*', 'g', 'a\\"b"c')
add(r'\s*,\s*', 'g', 'a , b,c ,d')
add(r'[\s,]+', 'g', 'a, b,,c d')
add(r'\b\w+\b', 'g', 'lorem ipsum dolor ' * 5)
add(r'\b\w+\b', 'g', UNI)
add(r'(?:(?:\r\n|\n)\s*){2,}', 'g', 'a\n\n\nb\n  \n c\r\n\r\nd')
add(r'<[^>]+>', 'g', '<p class="x">hi <b>there</b></p>')
add(r'&(?:amp|lt|gt|quot|#\d+);', 'g', 'a &amp; b &lt; &#39; &bad;')
add(r'(?:[^\s"]+|"[^"]*")+', 'g', 'run --flag "a b" c"d e"f')
add(r'^(?:[a-z0-9-]+\.)+[a-z]{2,}$', 'i', 'Sub.Example.COM', 'bad_domain.com')
add(r'^[\w-]+(?:\.[\w-]+)*$', '', 'a.b-c.d', 'a..b')
add(r'(\d{1,2}):(\d{2})(?::(\d{2}))?\s*(am|pm)?', 'gi', '9:30 AM, 14:05:59 and 7:00pm')
add(r'\b(\w)(\w*)\1\b', 'g', 'level noon abca xyx')
add(r'^(?:(?:\+|00)33|0)\s*[1-9](?:[\s.-]*\d{2}){4}$', '', '+33 6 12 34 56 78', '0612345678', '12345')
add(r'\b(?:https?|ftp):\/\/(?:www\.)?([^\/\s]+)', 'gi', 'see HTTP://WWW.Example.com/x and ftp://files.org')
add(r'[̀-ͯ]', 'g', 'éä')
add(r'\p{M}+', 'gu', 'éä')
add(r'[\p{Emoji_Presentation}]', 'u', 'x')
add(r'(?<word>\p{L}+)\s+\k<word>', 'giu', 'Привет привет мир')
add(r'(\p{Lu})(\p{Ll}+)', 'gu', 'Élan Vital ÖL')
add(r'[^\x00-\x7F]+', 'g', UNI)
add(r'[^\x00-\x7F]', 'gu', ASTRAL)
add(r'\w+@\w+\.com', 'g', 'contact: bob@example.com, alice@test.com')
add(r'(?:a|b|c|d|e)+x', '', 'abcdeabcdex')
add(r'(?:ab|cd|ef)*?g', '', 'abcdefg')
add(r'^(?:a|ab)(?:c|bcd)(?:d*)$', '', 'abcd')
add(r'(a|b)\1*', 'g', 'aaabbbab')
add(r'((a)|(b))*', '', 'aba')
add(r'(?:(a)|(b)|(c))+', '', 'abc', 'cba')
add(r'(a)*', '', 'aaa')
add(r'(a)*?b', '', 'aaab')
add(r'(?:(a)b)*c', '', 'ababc')
add(r'(a(b)?)+', '', 'aba')
add(r'(a(b)?)+', '', 'abab')
add(r'((a)|b)+?c', '', 'abc')
add(r'(?:x(a)?)+', '', 'xax')
add(r'(?:x(a)?){2}', '', 'xax')
add(r'(?:x(a)?){1,3}', '', 'xaxxa')
add(r'([ab]*?)(?=b)', 'g', 'aabab')
add(r'(?:a(?=b)|.)+', '', 'aab')
add(r'\b(\w+)(?:\s+\1\b)+', 'gi', 'go go GO now')
add(r'^(?:\s*(\w+)\s*,?)+$', '', 'a, b ,c')
add(r'(\s*)(\S+)', 'g', '  a bb   ccc')
add(r'(?:^|\s)(\w)(?=\w*$)', 'g', 'last word here')
add(r'[aeiou](?=[^aeiou]*$)', 'g', 'programming')
add(r'(?<=[aeiou])[^aeiou\s]', 'g', 'hello world')
add(r'\d{3}(?=(?:\d{3})+$)', 'g', '1234567890')
add(r'\B(?=(\d{3})+(?!\d))', 'g', '1234567 and 89')
add(r'(.)(?=.*\1)', 'g', 'abcabd')
add(r'a.*b', '', 'a' + 'x' * 100 + 'b' + 'y' * 10)
add(r'a.*?b', 'g', 'axxbyyb ab')
add(r'.*foo', '', 'xfooyfoo z')
add(r'.*?foo', '', 'xfooyfoo z')
add(r'[^"]*"', 'g', 'ab"cd"e')
add(r'[^)]*\)', '', '(a b) c)')
add(r'\w+\d', 'g', 'abc1 xyz22 q')
add(r'\w*\b', 'g', 'ab cd')
add(r'\w+$', '', 'foo bar')
add(r'\s*$', '', 'abc   ')
add(r'a*$', 'g', 'baaa')
add(r'x+$', 'm', 'xx\nx')
add(r'\w+\B', 'g', 'abc de f')
add(r'é+$', '', 'aéé')
add(r'[é]+\b', 'g', 'éé aé')
add(r'.{2,3}', 'g', 'ab😀cd')
add(r'.{2,3}', 'gu', 'ab😀cd')
add(r'\S+', 'gu', 'a😀 b👍🏽')
add(r'[😀-😃]', 'u', '😁')
add(r'^[^@]+@[^@]+$', '', 'a@b', 'a@b@c')

json.dump(cases, sys.stdout, ensure_ascii=False)
