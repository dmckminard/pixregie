// pixregie-agent watches local Claude Code sessions and reports their status
// to the Pixregie display.
//
// Without arguments on macOS and Windows it runs as a menu-bar / tray app.
// On servers, `pixregie-agent run` does the same headless.
package main

import (
	"fmt"
	"os"
)

var version = "dev" // set at build time with -ldflags "-X main.version=..."

func main() {
	if len(os.Args) < 2 || (len(os.Args) == 2 && os.Args[1] == "tray") {
		tray()
		return
	}
	switch os.Args[1] {
	case "scan":
		scan(os.Args[2:])
	case "run":
		run(os.Args[2:])
	case "serve":
		serve(os.Args[2:])
	case "pair":
		pair(os.Args[2:])
	case "hooks":
		hooksCmd(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println("pixregie-agent", version)
	case "-psn_0_0": // macOS Finder launch argument on old systems
		tray()
	default:
		if len(os.Args[1]) > 4 && os.Args[1][:5] == "-psn_" {
			tray()
			return
		}
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  pixregie-agent                    menu-bar app (macOS, Windows)
  pixregie-agent run [-server URL]  report in the background (display found with mDNS by default)
  pixregie-agent pair [-token]      pair with the code shown on the Pi's screen
  pixregie-agent serve              server mode: the Pi pulls through an SSH tunnel
  pixregie-agent hooks install|uninstall|status
  pixregie-agent scan [-json]       print what the agent sees, send nothing
  pixregie-agent version`)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "pixregie-agent:", err)
	os.Exit(1)
}
