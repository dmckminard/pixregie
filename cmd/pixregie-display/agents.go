package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
)

func editConfig(path string, f func(*Config) error) {
	cfg, err := load(path)
	if err != nil {
		log.Fatal(err)
	}
	if err := f(&cfg); err != nil {
		log.Fatal(err)
	}
	if err := save(path, cfg); err != nil {
		log.Fatal(err)
	}
}

func listAgents(args []string) {
	fs := flag.NewFlagSet("agents", flag.ExitOnError)
	path := configFlag(fs)
	fs.Parse(args)
	cfg, err := load(*path)
	if err != nil {
		log.Fatal(err)
	}
	type row struct{ label, kind, id string }
	var rows []row
	for hash, label := range cfg.Agents {
		rows = append(rows, row{label, "machine", hash[:8]})
	}
	for _, s := range cfg.Servers {
		rows = append(rows, row{s.Label, "server", s.URL})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].label < rows[j].label })
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tKIND\tID")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\n", r.label, r.kind, r.id)
	}
	w.Flush()
}

func removeAgent(args []string) {
	fs := flag.NewFlagSet("remove-agent", flag.ExitOnError)
	path := configFlag(fs)
	fs.Parse(args)
	label := strings.TrimSpace(strings.Join(fs.Args(), " "))
	editConfig(*path, func(c *Config) error {
		n := 0
		for hash, l := range c.Agents {
			if l == label || strings.HasPrefix(hash, label) && len(label) >= 8 {
				delete(c.Agents, hash)
				n++
			}
		}
		if n == 0 {
			return fmt.Errorf("no machine named %q (see `agents`)", label)
		}
		fmt.Printf("Revoked %d token(s) for %q.\n", n, label)
		return nil
	})
}

func renameAgent(args []string) {
	fs := flag.NewFlagSet("rename-agent", flag.ExitOnError)
	path := configFlag(fs)
	fs.Parse(args)
	if fs.NArg() != 2 {
		log.Fatal("usage: rename-agent OLD NEW (quote names with spaces)")
	}
	old, nu := fs.Arg(0), strings.TrimSpace(fs.Arg(1))
	if !labelRE.MatchString(nu) {
		log.Fatal("name: 1-32 chars, letters, digits, space, . _ -")
	}
	editConfig(*path, func(c *Config) error {
		n := 0
		for hash, l := range c.Agents {
			if l == old {
				c.Agents[hash] = nu
				n++
			}
		}
		for i := range c.Servers {
			if c.Servers[i].Label == old {
				c.Servers[i].Label = nu
				n++
			}
		}
		if n == 0 {
			return fmt.Errorf("no machine named %q", old)
		}
		fmt.Printf("Renamed %q to %q.\n", old, nu)
		return nil
	})
}

func addServer(args []string) {
	fs := flag.NewFlagSet("add-server", flag.ExitOnError)
	path := configFlag(fs)
	fs.Parse(args)
	if fs.NArg() != 2 {
		log.Fatal("usage: add-server LABEL URL   (token on stdin)")
	}
	label, raw := strings.TrimSpace(fs.Arg(0)), fs.Arg(1)
	if !labelRE.MatchString(label) {
		log.Fatal("label: 1-32 chars, letters, digits, space, . _ -")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Host == "" {
		log.Fatal("URL must be http://127.0.0.1:PORT (the tunnel's local end)")
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	tok := strings.TrimSpace(line)
	if !strings.HasPrefix(tok, "pxs_") || len(tok) < 20 {
		log.Fatal("this does not look like a server token (pxs_…)")
	}
	editConfig(*path, func(c *Config) error {
		kept := c.Servers[:0]
		for _, s := range c.Servers {
			if s.Label != label {
				kept = append(kept, s)
			}
		}
		c.Servers = append(kept, ServerConfig{Label: label, URL: raw, Token: tok})
		return nil
	})
	fmt.Printf("Server %q added.\n", label)
}

func removeServer(args []string) {
	fs := flag.NewFlagSet("remove-server", flag.ExitOnError)
	path := configFlag(fs)
	fs.Parse(args)
	label := strings.TrimSpace(strings.Join(fs.Args(), " "))
	editConfig(*path, func(c *Config) error {
		kept := c.Servers[:0]
		for _, s := range c.Servers {
			if s.Label != label {
				kept = append(kept, s)
			}
		}
		if len(kept) == len(c.Servers) {
			return fmt.Errorf("no server named %q", label)
		}
		c.Servers = kept
		return nil
	})
	fmt.Printf("Server %q removed.\n", label)
}
