package screen

import (
	"time"

	"github.com/dmckminard/pixregie/internal/collector"
	"github.com/dmckminard/pixregie/internal/session"
)

// Demo returns a state with every status, for tests and the preview tool.
func Demo(now time.Time) collector.State {
	titles := map[string]string{"api-gateway": "Refonte du cache", "mobile-app": "Écran de connexion", "data-sync": "Synchro des comptes"}
	s := func(p string, st session.Status, tool string, age time.Duration) session.Session {
		return session.Session{ID: p, Project: p, Title: titles[p], Status: st, Tool: tool, Modified: now.Add(-age)}
	}
	return collector.State{Generated: now, Machines: []collector.Machine{
		{Name: "Mac", Online: true, LastSeen: now, Sessions: []session.Session{
			s("api-gateway", session.Working, "Bash", 2*time.Second),
			s("mobile-app", session.Waiting, "", 40*time.Second),
			s("pixregie", session.Working, "Edit", time.Second),
		}},
		{Name: "PC", Online: true, LastSeen: now, Sessions: []session.Session{
			s("firmware", session.Idle, "", 20*time.Minute),
			s("blog", session.Working, "Write", 3*time.Second),
		}},
		{Name: "VPS", Online: true, LastSeen: now, Sessions: []session.Session{
			s("data-sync", session.Working, "", 5*time.Second),
			s("infra", session.Stale, "", 14*time.Minute),
		}},
	}}
}
