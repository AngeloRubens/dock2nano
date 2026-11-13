package main

import (
	"os"

	"github.com/AngeloRubens/dock2nano/cmd"
)

func main() {
	os.Exit(cmd.Run(os.Args[1:]))
}
