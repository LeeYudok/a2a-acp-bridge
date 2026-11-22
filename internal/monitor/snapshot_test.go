package monitor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LeeYudok/a2a-acp-bridge/internal/agents"
	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
)

func TestBuildDerivesAgentStateAndHidesMessageBodies(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	raw := board.Snapshot{
		Tasks: []board.Task{
			{TaskID: "task-codex", Assignee: "codex", Creator: "agy", Title: "bridge monitor", State: "working", SessionRef: "sess-codex", UpdatedAt: float64(now.Add(-time.Minute).UnixNano()) / 1e9},
			{TaskID: "task-agy-old", Assignee: "agy", Creator: "codex", Title: "stale job", State: "working", UpdatedAt: float64(now.Add(-10*time.Minute).UnixNano()) / 1e9},
			{TaskID: "task-claude", Assignee: "claude", Creator: "agy", Title: "failed review", State: "failed", UpdatedAt: float64(now.Add(-2*time.Minute).UnixNano()) / 1e9},
			{TaskID: "task-completed", Assignee: "codex", Creator: "agy", Title: "finished", State: "completed", UpdatedAt: float64(now.Add(-20*time.Minute).UnixNano()) / 1e9},
		},
		Messages: []board.Message{{MessageID: "msg-secret", Content: "SECRET PROMPT BODY", CreatedAt: float64(now.Unix())}},
		Sessions: []board.NamedSession{{Name: "review", Agent: "claude", SessionID: "sess-claude", Cwd: "/private/work", UpdatedAt: float64(now.Add(-time.Minute).UnixNano()) / 1e9}},
		Coordinator: &board.CoordinatorLease{
			Leader: "agy", Term: 3, LeaseUntil: float64(now.Add(time.Hour).UnixNano()) / 1e9,
			UpdatedAt: float64(now.Add(-time.Minute).UnixNano()) / 1e9, ActiveAgents: []string{"agy", "codex"},
		},
	}
	cards := []agents.Card{{ID: "agy", Name: "AGY"}, {ID: "codex", Name: "Codex"}, {ID: "claude", Name: "Claude"}}
	got := Build(raw, cards, now, 5*time.Minute)

	states := map[string]string{}
	for _, agent := range got.Agents {
		states[agent.ID] = agent.State
	}
	if states["agy"] != "stale" || states["codex"] != "working" || states["claude"] != "failed" {
		t.Fatalf("agent states = %#v, want agy=stale codex=working claude=failed", states)
	}
	if len(got.Tasks) != 4 || got.Tasks[0].TaskID != "task-codex" {
		t.Fatalf("tasks = %#v, want newest first", got.Tasks)
	}
	if got.Coordinator == nil || got.Coordinator.Status != "active" || got.Coordinator.Leader != "agy" {
		t.Fatalf("coordinator = %#v", got.Coordinator)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	body := string(encoded)
	for _, leaked := range []string{"SECRET PROMPT BODY", "bridge monitor", "stale job", "failed review", "/private/work", "sess-codex", "sess-claude", "msg-secret"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("monitor response leaked %q: %s", leaked, body)
		}
	}
}

func TestBuildReturnsEmptySlices(t *testing.T) {
	got := Build(board.Snapshot{}, []agents.Card{{ID: "codex", Name: "Codex"}}, time.Now(), 0)
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	for _, want := range []string{`"tasks":[]`, `"sessions":[]`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("JSON = %s, missing %s", encoded, want)
		}
	}
	if got.StaleAfterSeconds != int(DefaultStaleAfter/time.Second) {
		t.Fatalf("stale_after_seconds = %d, want %d", got.StaleAfterSeconds, int(DefaultStaleAfter/time.Second))
	}
}
