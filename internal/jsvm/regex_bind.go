package jsvm

import "github.com/paul-nameless/qi/internal/jsvm/regex"

func compileEngine(pattern, flags string) (reEngine, error) {
	re, err := regex.Compile(pattern, flags)
	if err != nil {
		return nil, err
	}
	return re, nil
}
