// pixregie-display runs on the Raspberry Pi: it collects session snapshots
// from the agents and serves the debug page. The pixel-art screen comes next.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/dmckminard/pixregie/internal/collector"
)

var version = "dev"

const defaultConfig = "/etc/pixregie/display.json"

// Config is stored with mode 0600. It holds hashes only, never raw secrets.
type Config struct {
	Listen        string            `json:"listen"`
	Agents        map[string]string `json:"agents"`         // sha256(token) -> machine label
	DebugPassword string            `json:"debug_password"` // pbkdf2 hash; empty = page off
}

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
	case "set-password":
		setPassword(args)
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
  pixregie-display set-password [-config FILE]         (reads the password on stdin)
  pixregie-display version`)
}

func configFlag(fs *flag.FlagSet) *string {
	return fs.String("config", defaultConfig, "configuration file")
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	path := configFlag(fs)
	fs.Parse(args)
	cfg, err := load(*path)
	if err != nil {
		log.Fatal(err)
	}
	logger := log.New(os.Stderr, "", log.LstdFlags)
	srv := &collector.Server{
		Store: collector.NewStore(30*time.Second, 24*time.Hour),
		Auth:  collector.Auth{AgentTokens: cfg.Agents, DebugPassword: cfg.DebugPassword},
		Log:   logger,
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
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		hs.Shutdown(sctx)
	}()
	logger.Printf("pixregie-display %s listening on %s (%d agents, debug page %s)",
		version, cfg.Listen, len(cfg.Agents), onOff(cfg.DebugPassword != ""))
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
	fmt.Println("Restart the service to apply: sudo systemctl restart pixregie-display")
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
	fmt.Fprintln(os.Stderr, "Saved. Restart the service to apply.")
}

func load(path string) (Config, error) {
	cfg := Config{Listen: ":7421", Agents: map[string]string{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	if cfg.Agents == nil {
		cfg.Agents = map[string]string{}
	}
	return cfg, nil
}

// save writes atomically with mode 0600.
func save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".display-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
