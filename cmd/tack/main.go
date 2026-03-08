package main

import (
	"context"
	"fmt"
	"os"

	"tack/internal/cli"
)

func main() {
	err := cli.Execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		_, writeErr := fmt.Fprintln(os.Stderr, err)
		if writeErr != nil {
			os.Exit(1)
		}

		os.Exit(1)
	}
}
