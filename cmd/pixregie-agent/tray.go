//go:build darwin || windows

package main

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"fyne.io/systray"

	"github.com/dmckminard/pixregie/internal/agent"
	"github.com/dmckminard/pixregie/internal/agentcfg"
	"github.com/dmckminard/pixregie/internal/discover"
	"github.com/dmckminard/pixregie/internal/hooks"
	"github.com/dmckminard/pixregie/internal/icon"
	"github.com/dmckminard/pixregie/internal/release"
	"github.com/dmckminard/pixregie/internal/session"
)

const hooksNote = "Pixregie ajoute aussi 3 hooks à Claude Code pour savoir quelles sessions sont ouvertes. " +
	"Ils ne transmettent que l'identifiant de session, jamais tes prompts ni ton code."

// hookLn is bound before anything else and held until exit: a second
// Pixregie cannot start while it is taken (single instance), even if the
// first one is still waiting on a dialog.
var hookLn net.Listener

func tray() {
	cleanupOldUpdate()
	// After an update the new copy starts while the old one is still
	// quitting: wait a little for the port before declaring a duplicate.
	var ln net.Listener
	var err error
	for i := 0; i < 10; i++ {
		if ln, err = hooks.Bind(); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		alert("Pixregie est déjà lancé : son icône est dans la barre des menus.")
		return
	}
	hookLn = ln
	systray.Run(onReady, func() {})
}

// iconFor renders the tray icon for a state, in the platform's format.
func iconFor(st session.Status) []byte {
	outline := color.RGBA{0x1c, 0x1a, 0x24, 0xff}
	if darkMenuBar() {
		outline = color.RGBA{0xe8, 0xe4, 0xf4, 0xff}
	}
	return platformIcon(icon.Image(st, 2, outline))
}

func onReady() {
	systray.SetIcon(iconFor(icon.Disconnected))
	systray.SetTooltip("Pixregie")

	mStatus := systray.AddMenuItem("Démarrage…", "")
	mStatus.Disable()
	mCounts := systray.AddMenuItem("", "")
	mCounts.Disable()
	systray.AddSeparator()
	mRefresh := systray.AddMenuItem("Rafraîchir", "Relire les sessions et rechercher le Pi")
	mDebug := systray.AddMenuItem("Ouvrir la page de debug", "")
	mLogin := systray.AddMenuItemCheckbox("Lancer au démarrage", "", autostartEnabled())
	systray.AddSeparator()
	mToken := systray.AddMenuItem("Associer à nouveau…", "")
	mUpdate := systray.AddMenuItem("Mettre à jour", "")
	mUpdate.Hide()
	mQuit := systray.AddMenuItem("Quitter Pixregie", "")
	mVersion := systray.AddMenuItem("version "+version, "")
	mVersion.Disable()

	// Quit must always work, even while a dialog is open or the Pi is
	// being searched for, so it does not wait for the main loop.
	go func() {
		<-mQuit.ClickedCh
		systray.Quit()
		time.Sleep(2 * time.Second)
		os.Exit(0) // in case a blocked dialog keeps the process alive
	}()

	// Everything else runs off the UI thread: dialogs and the event loop
	// block.
	go func() {
		loop(mStatus, mCounts, mRefresh, mDebug, mLogin, mToken, mUpdate)
	}()
}

func loop(mStatus, mCounts, mRefresh, mDebug, mLogin, mToken, mUpdate *systray.MenuItem) {
	cfg, _ := agentcfg.Load()
	token, err := agentcfg.GetToken()

	if err != nil {
		mStatus.SetTitle("○ Association avec le Pi…")
		t, ok := pairFlow(cfg.Server, true)
		if !ok {
			systray.Quit()
			return
		}
		token = t
	}

	settings, _ := hooks.SettingsPath()
	if _, err := hooks.Install(settings); err != nil {
		log.Printf("hooks: %v", err)
	}
	// Launch at login by default, once: if the user turns it off later,
	// that choice sticks.
	if !cfg.AutostartSet {
		if autostartEnabled() || setAutostart(true) == nil {
			mLogin.Check()
			cfg.AutostartSet = true
			agentcfg.Save(cfg)
		}
	}

	statePath, _ := agentcfg.StatePath()
	tracker := hooks.NewTracker(statePath)
	tracker.Prune()
	go func() {
		if err := hooks.Serve(hookLn, tracker); err != nil {
			log.Printf("hook listener: %v", err)
		}
	}()

	start := func(tok string) (*agent.Agent, context.CancelFunc) {
		a, err := agent.New(agent.Config{Server: cfg.Server, Token: tok, Tracker: tracker})
		if err != nil {
			alert(err.Error())
			os.Exit(1)
		}
		ctx, cancel := context.WithCancel(context.Background())
		go a.Run(ctx)
		return a, cancel
	}
	a, stop := start(token)

	render := func() {
		st := a.Status()
		line := "○ Recherche du Pi…"
		switch {
		case st.Connected:
			line = "● Connecté à " + hostOf(st.Server)
		case st.Err != "":
			line = "○ " + st.Err
		}
		mStatus.SetTitle(line)
		mCounts.SetTitle(fmt.Sprintf("%d en cours · %d attend · %d figée",
			st.Counts[session.Working], st.Counts[session.Waiting], st.Counts[session.Stale]))
		if st.Server != "" {
			mDebug.Enable()
		} else {
			mDebug.Disable()
		}
		state := st.Worst()
		if !st.Connected {
			state = icon.Disconnected
		}
		systray.SetIcon(iconFor(state))
		systray.SetTooltip("Pixregie · " + line)
	}

	// Updates: checked shortly after start, then every 6 hours. Installing
	// is the user's choice, from the menu.
	updates := make(chan string, 1)
	go watchUpdates(updates)
	pending := ""

	// Menu-bar appearance can change (dark mode at sunset): redraw the
	// icon from time to time even when nothing else changes.
	tick := time.NewTicker(time.Minute)
	for {
		select {
		case tag := <-updates:
			pending = tag
			mUpdate.SetTitle("Mettre à jour vers " + tag)
			mUpdate.Show()
		case <-mUpdate.ClickedCh:
			if pending == "" {
				continue
			}
			mUpdate.SetTitle("Mise à jour en cours…")
			mUpdate.Disable()
			if err := installUpdate(pending); err != nil {
				alert("Mise à jour impossible : " + err.Error())
				mUpdate.SetTitle("Mettre à jour vers " + pending)
				mUpdate.Enable()
				continue
			}
			systray.Quit()
			time.Sleep(time.Second)
			os.Exit(0)
		case <-a.Changed():
			render()
		case <-tick.C:
			render()
		case <-mRefresh.ClickedCh:
			a.Refresh()
		case <-mDebug.ClickedCh:
			if s := a.Status().Server; s != "" {
				openURL(s)
			}
		case <-mLogin.ClickedCh:
			want := !mLogin.Checked()
			if err := setAutostart(want); err != nil {
				alert("Impossible de modifier le lancement au démarrage : " + err.Error())
			} else if want {
				mLogin.Check()
			} else {
				mLogin.Uncheck()
			}
		case <-mToken.ClickedCh:
			if t, ok := pairFlow(cfg.Server, false); ok {
				stop()
				a, stop = start(t)
				render()
			}
		}
	}
}

// pairFlow finds the Pi and pairs with the code shown on its screen. If
// the Pi cannot be found or paired, it falls back to pasting a token
// created on the Pi. The token is stored on success.
func pairFlow(server string, first bool) (string, bool) {
	intro := ""
	if first {
		intro = "Bienvenue ! " + hooksNote + "\n\n"
	}
	if server == "" {
		// On macOS the first network access triggers the "local network"
		// permission prompt and fails while it is open: keep looking for a
		// minute instead of giving up on the first try.
		found, err := "", error(nil)
		for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			found, err = discover.Find(ctx, 5*time.Second)
			cancel()
			if err == nil {
				break
			}
			time.Sleep(2 * time.Second)
		}
		if err != nil {
			return askToken(intro + "Pi introuvable sur le réseau. Colle un token créé sur le Pi avec :\n" +
				"sudo bash install-pi.sh --add-agent NOM")
		}
		server = found
	}
	// The code is on the Pi's screen as soon as Pair starts; the first
	// dialog carries the welcome text.
	tok, err := agent.Pair(context.Background(), server, computerName(), func(retry bool) (string, bool) {
		msg := intro + "Tape le code affiché sur l'écran du Pi :"
		if retry {
			msg = "Code incorrect. Tape le code affiché sur l'écran du Pi :"
		}
		return prompt(msg, false)
	})
	if errors.Is(err, agent.ErrPairCancelled) {
		return "", false
	}
	if err != nil {
		alert("Association impossible : " + err.Error())
		return "", false
	}
	if err := agentcfg.SetToken(tok); err != nil {
		alert("Impossible d'enregistrer le token : " + err.Error())
		return "", false
	}
	return tok, true
}

func watchUpdates(found chan<- string) {
	c := &http.Client{Timeout: 30 * time.Second}
	time.Sleep(time.Minute)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		tag, err := release.Latest(ctx, c)
		cancel()
		if err == nil && release.Newer(tag, version) {
			select {
			case found <- tag:
			default:
			}
		}
		time.Sleep(6 * time.Hour)
	}
}

// installUpdate downloads the platform asset of tag, which must be listed
// in a SHA256SUMS signed with the release key, and swaps it in.
func installUpdate(tag string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	data, err := release.Download(ctx, &http.Client{Timeout: 5 * time.Minute}, tag, updateAsset)
	if err != nil {
		return err
	}
	return applyUpdate(data)
}

// askToken shows the platform dialog until a well-formed token is entered
// or the user cancels, then stores it.
func askToken(msg string) (string, bool) {
	for {
		t, ok := prompt(msg, true)
		if !ok {
			return "", false
		}
		if agentcfg.ValidToken(t) {
			if err := agentcfg.SetToken(t); err != nil {
				alert("Impossible d'enregistrer le token : " + err.Error())
				return "", false
			}
			return t, true
		}
		msg = "Ce n'est pas un token Pixregie (il commence par pxr_). Réessaie :"
	}
}

func hostOf(u string) string {
	if h, _, err := net.SplitHostPort(trimScheme(u)); err == nil {
		return h
	}
	return trimScheme(u)
}

func trimScheme(u string) string {
	for _, p := range []string{"http://", "https://"} {
		if len(u) > len(p) && u[:len(p)] == p {
			return u[len(p):]
		}
	}
	return u
}
