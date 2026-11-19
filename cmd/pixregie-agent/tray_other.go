//go:build !darwin && !windows

package main

import (
	"fmt"
	"os"
)

func tray() {
	fmt.Fprintln(os.Stderr, "the menu-bar app exists on macOS and Windows; use `pixregie-agent run` here")
	usage()
	os.Exit(2)
}
