package agent

import (
	"os"
	"testing"

	"github.com/paul-nameless/qi/internal/testenv"
)

func TestMain(m *testing.M) {
	os.Exit(testenv.Run(m))
}
