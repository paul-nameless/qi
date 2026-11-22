package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/paul-nameless/qi/internal/jsvm/bench/harness"
	"github.com/paul-nameless/qi/internal/jsvm/bench/oracle"
	"github.com/paul-nameless/qi/internal/jsvm/bench/pool"
)

func stateRecs(engine string) ([]oracle.StateStep, error) {
	bin, err := pool.Build(engine)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	cmd := exec.Command(bin, "-json", "state")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s state: %w", engine, err)
	}
	var recs []harness.Rec
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(l, "@@") {
			var r harness.Rec
			if err := json.Unmarshal([]byte(l[2:]), &r); err != nil {
				return nil, err
			}
			recs = append(recs, r)
		}
	}
	if len(recs) == 0 {
		return nil, fmt.Errorf("%s state: no json", engine)
	}
	return oracle.StateSteps(recs), nil
}

func stateExpect(args []string) error {
	g, err := stateRecs("goja")
	if err != nil {
		return err
	}
	q, err := stateRecs("modernc")
	if err != nil {
		return err
	}
	for i := range g {
		switch {
		case i >= len(q):
			fmt.Printf("differs %s: goja %+v quickjs <missing>\n", g[i].Name, g[i])
		case g[i] != q[i]:
			fmt.Printf("differs %s: goja %+v quickjs %+v\n", g[i].Name, g[i], q[i])
		}
	}
	b, _ := json.MarshalIndent(g, "", " ")
	return os.WriteFile("corpus/state.expected.json", append(b, '\n'), 0o644)
}
