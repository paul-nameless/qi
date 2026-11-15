package app

import (
	"os"
	"testing"

	"github.com/paul-nameless/qi/internal/mcp/mcptest"
	"github.com/paul-nameless/qi/internal/testenv"
)

func TestMain(m *testing.M) {
	if os.Getenv(mcptest.Env) == "1" {
		mcptest.Serve(os.Stdin, os.Stdout)
		return
	}
	os.Exit(testenv.Run(m))
}
