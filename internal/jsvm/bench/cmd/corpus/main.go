package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: corpus record|replay-expect|accept|tag ...")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "record":
		err = recordAll(os.Args[2:])
	case "replay-expect":
		err = replayExpect(os.Args[2:])
	case "state-expect":
		err = stateExpect(os.Args[2:])
	case "tag":
		err = tagAll(os.Args[2:])
	case "accept":
		err = accept(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "corpus:", err)
		os.Exit(1)
	}
}
