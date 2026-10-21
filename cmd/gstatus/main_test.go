package main

import (
	"bytes"
	"testing"
)

func TestVersion(t *testing.T) {
	var output bytes.Buffer

	if err := run([]string{"--version"}, &output); err != nil {
		t.Fatal(err)
	}

	if got := output.String(); got != "gstatus dev\n" {
		t.Fatalf("version output = %q", got)
	}
}

func TestUnknownArgument(t *testing.T) {
	var output bytes.Buffer

	if err := run([]string{"--unknown"}, &output); err == nil {
		t.Fatal("unsupported option was accepted")
	}
}
