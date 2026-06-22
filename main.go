// Package main contains the dots command-line entry point.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/2h2d-co/dots/internal/dots"
)

var version = "dev"

func main() {
	cmd := dots.NewRootCommand(version)
	if err := cmd.Execute(); err != nil {
		var exitErr dots.ExitError
		code := 1
		if errors.As(err, &exitErr) {
			code = exitErr.Code
			if exitErr.Silent {
				os.Exit(code)
			}
		}
		fmt.Fprintln(os.Stderr, "Error:", err.Error())
		os.Exit(code)
	}
}
