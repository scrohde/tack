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
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
