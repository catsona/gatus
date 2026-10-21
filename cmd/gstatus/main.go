package main

import (
	"fmt"
	"io"
	"os"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "gstatus:", err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	if len(args) == 1 {
		switch args[0] {
		case "--version":
			_, err := fmt.Fprintf(output, "gstatus %s\n", version)
			return err

		case "--help", "-h":
			_, err := fmt.Fprintln(output, "Usage: gstatus [--version | --help]")
			return err
		}
	}

	if len(args) != 0 {
		return fmt.Errorf("unsupported arguments: %v", args)
	}

	return fmt.Errorf("the monitor is not available in this development build")
}
