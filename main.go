package main

import (
	"fmt"
	"github.com/aeon022/missionctl-core/secure"
	"os"

	"github.com/aeon022/budgetctl/cmd"
)

func main() {
	secure.Init("budgetctl") // owner-only files from the first byte
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
