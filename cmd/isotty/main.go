package main

import (
	"fmt"
	"os"

	"github.com/yazawa/isotty/internal/isotty"
)

func main() {
	app := isotty.NewApp()
	if err := app.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "isotty: %v\n", err)
		os.Exit(1)
	}
}
