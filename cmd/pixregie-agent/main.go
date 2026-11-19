// pixregie-agent watches local Claude Code sessions and reports their status.
//
// `scan` prints the sessions seen on this machine; `run` reports them to the
// display every few seconds. The tray icon and mDNS discovery come next.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/dmckminard/pixregie/internal/session"
)

var version = "dev" // set at build time with -ldflags "-X main.version=..."

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "scan":
		scan(os.Args[2:])
	case "run":
		run(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println("pixregie-agent", version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: pixregie-agent scan [-json] [-within 24h] [-root DIR]
       pixregie-agent run -server http://pixregie.local:7421   (token in $PIXREGIE_TOKEN)
       pixregie-agent version`)
}

func scan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	within := fs.Duration("within", 24*time.Hour, "only sessions active within this duration")
	stale := fs.Duration("stale", 10*time.Minute, "working sessions silent this long are shown as stale")
	root := fs.String("root", "", "transcripts directory (default ~/.claude/projects)")
	fs.Parse(args)

	if *root == "" {
		r, err := session.DefaultRoot()
		if err != nil {
			fatal(err)
		}
		*root = r
	}
	host, _ := os.Hostname()
	sessions, err := session.Scan(*root, *within, session.Options{
		Machine: host, StaleAfter: *stale, Now: time.Now(),
	})
	if err != nil {
		fatal(err)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(sessions)
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STATUS\tPROJECT\tBRANCH\tTOOL\tLAST ACTIVITY\tSESSION")
	for _, s := range sessions {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s ago\t%.8s\n",
			s.Status, s.Project, s.Branch, s.Tool,
			time.Since(s.Modified).Round(time.Second), s.ID)
	}
	w.Flush()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "pixregie-agent:", err)
	os.Exit(1)
}
