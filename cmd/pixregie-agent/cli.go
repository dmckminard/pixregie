package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/dmckminard/pixregie/internal/agent"
	"github.com/dmckminard/pixregie/internal/agentcfg"
	"github.com/dmckminard/pixregie/internal/hooks"
	"github.com/dmckminard/pixregie/internal/session"
)

func scan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	within := fs.Duration("within", 24*time.Hour, "only sessions active within this duration")
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
	sessions, err := session.Scan(*root, *within, session.Options{Machine: host, StaleAfter: 10 * time.Minute, Now: time.Now()})
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
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s ago\t%.8s\n", s.Status, s.Project, s.Branch, s.Tool,
			time.Since(s.Modified).Round(time.Second), s.ID)
	}
	w.Flush()
}

// run is the headless agent (servers, or before the tray app existed).
// The token comes from PIXREGIE_TOKEN or `pixregie-agent pair`, never from
// a flag, so it does not appear in the process list.
func run(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	server := fs.String("server", "", "display URL (default: found with mDNS)")
	every := fs.Duration("every", 3*time.Second, "report interval")
	root := fs.String("root", "", "transcripts directory (default ~/.claude/projects)")
	fs.Parse(args)

	token := strings.TrimSpace(os.Getenv("PIXREGIE_TOKEN"))
	if token == "" {
		t, err := agentcfg.GetToken()
		if err != nil {
			fatal(errors.New("no token: run `pixregie-agent pair` or set PIXREGIE_TOKEN"))
		}
		token = t
	}
	if *server == "" {
		cfg, _ := agentcfg.Load()
		*server = cfg.Server
	}

	statePath, _ := agentcfg.StatePath()
	tracker := hooks.NewTracker(statePath)
	go func() {
		if err := hooks.Listen(tracker); err != nil {
			log.Printf("hook listener disabled (%v): open/closed and permission states come from transcripts only", err)
		}
	}()

	a, err := agent.New(agent.Config{Server: *server, Token: token, Root: *root, Every: *every, Tracker: tracker})
	if err != nil {
		fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go a.Run(ctx)

	last := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.Changed():
			st := a.Status()
			msg := "reporting to " + st.Server
			if !st.Connected {
				msg = "not reporting: " + st.Err
			}
			if msg != last { // log transitions only
				log.Print(msg)
				last = msg
			}
		}
	}
}

func pair(args []string) {
	fmt.Fprint(os.Stderr, "Token (pxr_…): ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	tok := strings.TrimSpace(line)
	if !agentcfg.ValidToken(tok) {
		fatal(errors.New("this does not look like a Pixregie token"))
	}
	if err := agentcfg.SetToken(tok); err != nil {
		fatal(err)
	}
	fmt.Fprintln(os.Stderr, "Token saved.")
}

func hooksCmd(args []string) {
	path, err := hooks.SettingsPath()
	if err != nil {
		fatal(err)
	}
	if len(args) != 1 {
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "install":
		changed, err := hooks.Install(path)
		if err != nil {
			fatal(err)
		}
		fmt.Println(map[bool]string{true: "Hooks installed in " + path, false: "Hooks already installed"}[changed])
	case "uninstall":
		changed, err := hooks.Uninstall(path)
		if err != nil {
			fatal(err)
		}
		fmt.Println(map[bool]string{true: "Hooks removed from " + path, false: "No Pixregie hooks found"}[changed])
	case "status":
		fmt.Println(map[bool]string{true: "installed", false: "not installed"}[hooks.Installed(path)])
	default:
		usage()
		os.Exit(2)
	}
}
