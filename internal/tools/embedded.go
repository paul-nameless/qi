package tools

import (
	"slices"
	"strings"
)

const EmbeddedPrefix = "qi:"

var embedded = map[string][]byte{}

func Embed(path string, data []byte) {
	embedded[path] = data
}

func Embedded(path string) ([]byte, bool) {
	data, ok := embedded[path]
	return data, ok
}

func builtinWrite(path string) (Result, bool) {
	if !strings.HasPrefix(path, EmbeddedPrefix) {
		return Result{}, false
	}
	return deniedResult("%s is built into qi and read-only", path), true
}

func readEmbedded(path string, offset, limit int, crop []int) (Result, bool) {
	if !strings.HasPrefix(path, EmbeddedPrefix) {
		return Result{}, false
	}
	if len(crop) > 0 {
		return errResult("crop only applies to images"), true
	}
	if data, ok := embedded[path]; ok {
		return rawFile(data, offset, limit), true
	}
	var names []string
	for name := range embedded {
		names = append(names, name)
	}
	slices.Sort(names)
	return errResult("%s is not one of qi's built-in files: %s", path, strings.Join(names, ", ")), true
}
