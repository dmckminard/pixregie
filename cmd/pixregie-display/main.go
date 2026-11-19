// pixregie-display runs on the Raspberry Pi: it collects session snapshots
// from the agents (pushed over the LAN, or pulled from servers through SSH
// tunnels), draws the pixel-art board and serves the debug page.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/dmckminard/pixregie/internal/collector"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "serve":
		serve(args)
	case "add-agent":
		addAgent(args)
	case "agents":
		listAgents(args)
	case "remove-agent":
		removeAgent(args)
	case "rename-agent":
		renameAgent(args)
	case "add-server":
		addServer(args)
	case "remove-server":
		removeServer(args)
	case "set-password":
		setPassword(args)
	case "render":
		renderPNG(args)
	case "version", "--version", "-v":
		fmt.Println("pixregie-display", version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  pixregie-display serve        [-config FILE]
  pixregie-display add-agent    [-config FILE] LABEL   (prints a new token once)
  pixregie-display agents       [-config FILE]         (paired machines)
  pixregie-display remove-agent [-config FILE] LABEL   (revoke a machine)
  pixregie-display rename-agent [-config FILE] OLD NEW
  pixregie-display add-server   [-config FILE] LABEL URL   (token read on stdin)
  pixregie-display remove-server [-config FILE] LABEL
  pixregie-display set-password [-config FILE]         (reads the password on stdin)
  pixregie-display render       -o FILE.png [-night]    (demo picture, no Pi needed)
  pixregie-display version`)
}

func configFlag(fs *flag.FlagSet) *string {
	return fs.String("config", defaultConfig, "configuration file")
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	path := configFlag(fs)
	fbDev := fs.String("fb", "/dev/fb0", "framebuffer device (empty: no screen output)")
	fs.Parse(args)
	cfg, err := load(*path)
	if err != nil {
		log.Fatal(err)
	}
	logger := log.New(os.Stderr, "", log.LstdFlags)
	if cfg.BoardID == "" {
		id, err := collector.NewBoardID()
		if err != nil {
			log.Fatal(err)
		}
		editConfigErr(*path, func(c *Config) { c.BoardID = id })
		cfg.BoardID = id
	}
	store := collector.NewStore(30*time.Second, 24*time.Hour)
	pairer := collector.NewPairer()
	painter := &painter{store: store, pairer: pairer, fbDev: *fbDev, log: logger}
	srv := &collector.Server{
		Store:   store,
		Auth:    collector.Auth{AgentTokens: cfg.Agents, DebugPassword: cfg.DebugPassword},
		Log:     logger,
		Extra:   map[string]http.Handler{"/screen.png": http.HandlerFunc(painter.servePNG)},
		Pairer:  pairer,
		BoardID: cfg.BoardID,
		// Re-read the file before adding, so agents added meanwhile with
		// `add-agent` are not lost.
		SaveAgent: func(hash, label string) error {
			c, err := load(*path)
			if err != nil {
				return err
			}
			c.Agents[hash] = label
			return save(*path, c)
		},
	}
	hs := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	painter.sleepAfter = cfg.sleepAfter()
	painter.hideTitles = !cfg.showTitles()
	puller := newPuller(store, logger)
	puller.set(cfg.Servers)
	go puller.run(ctx)
	go watchConfig(ctx, *path, logger, func(c Config) {
		srv.SetAgents(c.Agents)
		puller.set(c.Servers)
		store.Retain(c.labels())
		painter.setSleepAfter(c.sleepAfter())
		painter.setHideTitles(!c.showTitles())
	})
	go painter.run(ctx)
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		hs.Shutdown(sctx)
	}()
	logger.Printf("pixregie-display %s listening on %s (%d agents, %d servers, debug page %s)",
		version, cfg.Listen, len(cfg.Agents), len(cfg.Servers), onOff(cfg.DebugPassword != ""))
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

var labelRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,31}$`)

func addAgent(args []string) {
	fs := flag.NewFlagSet("add-agent", flag.ExitOnError)
	path := configFlag(fs)
	fs.Parse(args)
	label := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if !labelRE.MatchString(label) {
		log.Fatal("label: 1-32 chars, letters, digits, space, . _ -")
	}
	cfg, err := load(*path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Fatal(err)
	}
	tok, err := collector.NewToken()
	if err != nil {
		log.Fatal(err)
	}
	cfg.Agents[collector.HashSecret(tok)] = label
	if err := save(*path, cfg); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Agent %q added. Token (shown once, copy it now):\n%s\n", label, tok)
}

func setPassword(args []string) {
	fs := flag.NewFlagSet("set-password", flag.ExitOnError)
	path := configFlag(fs)
	fs.Parse(args)
	cfg, err := load(*path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Fatal(err)
	}
	fmt.Fprint(os.Stderr, "Debug page password (min 10 chars): ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	pw := strings.TrimRight(line, "\r\n")
	if len(pw) < 10 {
		log.Fatal("password too short")
	}
	if cfg.DebugPassword, err = collector.HashPassword(pw); err != nil {
		log.Fatal(err)
	}
	if err := save(*path, cfg); err != nil {
		log.Fatal(err)
	}
	fmt.Fprintln(os.Stderr, "Saved.")
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
