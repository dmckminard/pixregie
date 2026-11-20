// pixregie-display runs on the Raspberry Pi: it collects session snapshots
// from the agents (pushed over the LAN, or pulled from servers through SSH
// tunnels), draws the pixel-art board and serves the debug page.
package main

import (
	"bufio"
	"context"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/dmckminard/pixregie/internal/collector"
	"github.com/dmckminard/pixregie/internal/tlspin"
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
	case "fingerprint":
		fingerprint(os.Args[2:])
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
  pixregie-display fingerprint  [-config FILE]         (the certificate machines pin)
  pixregie-display render       -o FILE.png [-night]    (demo picture, no Pi needed)
  pixregie-display version`)
}

// certHosts are the names the board answers to, written in its certificate
// (only informative: agents pin the certificate itself).
func certHosts() []string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return []string{"pixregie.local"}
	}
	return []string{h, h + ".local"}
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	painter.sleepAfter = cfg.sleepAfter()
	painter.hideTitles = !cfg.showTitles()
	puller := newPuller(store, logger)
	puller.set(cfg.Servers)
	go puller.run(ctx)
	applyCfg := func(c Config) {
		srv.SetAgents(c.Agents)
		puller.set(c.Servers)
		store.Retain(c.labels())
		var servers []string
		for _, s := range c.Servers {
			servers = append(servers, s.Label)
		}
		store.SetServers(servers)
		painter.setSleepAfter(c.sleepAfter())
		painter.setHideTitles(!c.showTitles())
	}
	go watchConfig(ctx, *path, logger, applyCfg)
	upg := &upgrader{request: filepath.Join(filepath.Dir(*path), upgradeRequest), status: upgradeStatus, kick: make(chan struct{}, 1)}
	go upg.watch(ctx)
	adm := &admin{path: *path, store: store, apply: applyCfg, upgrade: upg}
	srv.Extra["/v1/admin"] = http.HandlerFunc(adm.get)
	srv.AdminPost = map[string]http.Handler{
		"/v1/admin/settings": http.HandlerFunc(adm.settings),
		"/v1/admin/rename":   http.HandlerFunc(adm.rename),
		"/v1/admin/revoke":   http.HandlerFunc(adm.revoke),
		"/v1/admin/upgrade":  http.HandlerFunc(upg.post),
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
	cert, fp, err := tlspin.LoadOrCreate(filepath.Join(filepath.Dir(*path), "tls.crt"), filepath.Join(filepath.Dir(*path), "tls.key"), certHosts())
	if err != nil {
		log.Fatalf("TLS certificate: %v", err)
	}
	hs.TLSConfig = tlspin.ServerConfig(cert)
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Fatal(err)
	}
	go painter.run(ctx)
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		hs.Shutdown(sctx)
	}()
	logger.Printf("pixregie-display %s listening on https://%s (%d agents, %d servers, debug page %s, certificate %s)",
		version, cfg.Listen, len(cfg.Agents), len(cfg.Servers), onOff(cfg.DebugPassword != ""), fp)
	if err := hs.ServeTLS(tlspin.Split(ln), "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

// fingerprint prints the certificate's SHA-256, to compare with what a
// machine shows (or the browser's certificate details).
func fingerprint(args []string) {
	fs := flag.NewFlagSet("fingerprint", flag.ExitOnError)
	path := configFlag(fs)
	fs.Parse(args)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(*path), "tls.crt"))
	if err != nil {
		log.Fatal("no certificate yet (created when the service starts): ", err)
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		log.Fatal("invalid certificate file")
	}
	fmt.Println(tlspin.Fingerprint(blk.Bytes))
}
