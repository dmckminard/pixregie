//go:build darwin || windows

package main

import (
	"context"
	"fmt"
	"image/color"
	"log"
	"net"
	"os"
	"strconv"
	"time"

	"fyne.io/systray"

	"github.com/dmckminard/pixregie/internal/agent"
	"github.com/dmckminard/pixregie/internal/agentcfg"
	"github.com/dmckminard/pixregie/internal/hooks"
	"github.com/dmckminard/pixregie/internal/icon"
	"github.com/dmckminard/pixregie/internal/session"
)

const pairPrompt = "Colle le token affiché par le Pi (il commence par pxr_).\n\n" +
	"Pixregie ajoute aussi 3 hooks à Claude Code pour savoir quelles sessions sont ouvertes. " +
	"Ils ne transmettent que l'identifiant de session, jamais tes prompts ni ton code."

func tray() {
	// The hook port doubles as a single-instance lock.
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(hooks.Port)))
	if err != nil {
		alert("Pixregie est déjà lancé (voir la barre des menus).")
		return
	}
	ln.Close()
	systray.Run(onReady, func() {})
}

// iconFor renders the tray icon for a state, in the platform's format.
func iconFor(st session.Status) []byte {
	outline := color.RGBA{0x1c, 0x1a, 0x24, 0xff}
	if darkMenuBar() {
		outline = color.RGBA{0xe8, 0xe4, 0xf4, 0xff}
	}
	return platformIcon(icon.PNG(st, 2, outline))
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
	mToken := systray.AddMenuItem("Changer de token…", "")
	mQuit := systray.AddMenuItem("Quitter Pixregie", "")
	mVersion := systray.AddMenuItem("version "+version, "")
	mVersion.Disable()

	// Everything else runs off the UI thread: dialogs and the event loop
	// block.
	go func() {
		loop(mStatus, mCounts, mRefresh, mDebug, mLogin, mToken, mQuit)
	}()
}

func loop(mStatus, mCounts, mRefresh, mDebug, mLogin, mToken, mQuit *systray.MenuItem) {
	token, err := agentcfg.GetToken()
	firstRun := false
	if err != nil {
		t, ok := askToken(pairPrompt)
		if !ok {
			systray.Quit()
			return
		}
		token, firstRun = t, true
	}

	settings, _ := hooks.SettingsPath()
	if _, err := hooks.Install(settings); err != nil {
		log.Printf("hooks: %v", err)
	}
	if firstRun && !autostartEnabled() {
		if setAutostart(true) == nil {
			mLogin.Check()
		}
	}

	statePath, _ := agentcfg.StatePath()
	tracker := hooks.NewTracker(statePath)
	tracker.Prune()
	go func() {
		if err := hooks.Listen(tracker); err != nil {
			log.Printf("hook listener: %v", err)
		}
	}()

	cfg, _ := agentcfg.Load()
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

	// Menu-bar appearance can change (dark mode at sunset): redraw the
	// icon from time to time even when nothing else changes.
	tick := time.NewTicker(time.Minute)
	for {
		select {
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
			if t, ok := askToken("Nouveau token (pxr_…), généré sur le Pi avec :\nsudo bash install-pi.sh --add-agent NOM"); ok {
				stop()
				a, stop = start(t)
				render()
			}
		case <-mQuit.ClickedCh:
			stop()
			systray.Quit()
			return
		}
	}
}

// askToken shows the platform dialog until a well-formed token is entered
// or the user cancels, then stores it.
func askToken(msg string) (string, bool) {
	for {
		t, ok := promptToken(msg)
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
