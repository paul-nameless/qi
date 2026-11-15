package main

import (
	"os"

	"github.com/paul-nameless/qi/internal/app"
)

var version = "dev"

func main() {
	os.Exit(app.Main(os.Args[1:], version))
}
