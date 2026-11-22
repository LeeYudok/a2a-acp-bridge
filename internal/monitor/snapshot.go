// Package monitor builds a read-only A2A board view for external dashboards.
package monitor

import (
	"sort"
	"strings"
	"time"

	"github.com/LeeYudok/a2a-acp-bridge/internal/agents"
	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
)

const (
	// DefaultStaleAfter is the point where a working task is reported as stale.
	DefaultStaleAfter = 5 * time.Minute
	taskLimit         = 50
	sessionLimit      = 30
)

// Agent is the monitor view of one registered agent.
type Agent struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Version           string `json:"version,omitempty"`
	State             string `json:"state"`
	CurrentTaskID     string `json:"current_task_id,omitempty"`
	CurrentSession    string `json:"current_session,omitempty"`
	LastActivityAt    string `json:"last_activity_at,omitempty"`
	LastCoordinatorAt string `json:"last_coordinator_at,omitempty"`
	IsCoordinator     bool   `json:"is_coordinator"`
}

// Task is the safe monitor view of a board task. Titles and message bodies are omitted because
// delegated messages are copied into task titles by the bridge.
type Task struct {
	TaskID    string `json:"task_id"`
	State     string `json:"state"`
	Creator   string `json:"creator"`
	Assignee  string `json:"assignee"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// Session is the monitor view of a named session. Cwd and session IDs are omitted.
type Session struct {
	Name      string `json:"name"`
	Agent     string `json:"agent"`
	Mode      string `json:"mode,omitempty"`
	Model     string `json:"model,omitempty"`
	Closed    bool   `json:"closed"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// Coordinator is the monitor view of the orchestration lease.
type Coordinator struct {
	Status       string   `json:"status"`
	Leader       string   `json:"leader,omitempty"`
	Term         uint64   `json:"term,omitempty"`
	LeaseUntil   string   `json:"lease_until,omitempty"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
	ActiveAgents []string `json:"active_agents"`
}

// Snapshot is a read-only monitoring response. Empty collections are encoded as [].
type Snapshot struct {
	GeneratedAt       string       `json:"generated_at"`
	StaleAfterSeconds int          `json:"stale_after_seconds"`
	Agents            []Agent      `json:"agents"`
	Tasks             []Task       `json:"tasks"`
	Sessions          []Session    `json:"sessions"`
	Coordinator       *Coordinator `json:"coordinator,omitempty"`
}

// Capture reads the shared board without changing it and builds a monitoring snapshot.
func Capture(b *board.Board, now time.Time, staleAfter time.Duration) (Snapshot, error) {
	raw, err := b.Snapshot()
	if err != nil {
		return Snapshot{}, err
	}
	return Build(raw, agents.Cards(), now, staleAfter), nil
}

// Build converts board and registry data into the public monitoring DTO.
func Build(raw board.Snapshot, cards []agents.Card, now time.Time, staleAfter time.Duration) Snapshot {
	if staleAfter <= 0 {
		staleAfter = DefaultStaleAfter
	}
	now = now.UTC()
	out := Snapshot{
		GeneratedAt:       now.Format(time.RFC3339Nano),
		StaleAfterSeconds: int(staleAfter / time.Second),
		Agents:            make([]Agent, 0, len(cards)),
		Tasks:             make([]Task, 0, min(len(raw.Tasks), taskLimit)),
		Sessions:          make([]Session, 0, min(len(raw.Sessions), sessionLimit)),
	}

	tasks := append([]board.Task(nil), raw.Tasks...)
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].UpdatedAt != tasks[j].UpdatedAt {
			return tasks[i].UpdatedAt > tasks[j].UpdatedAt
		}
		return tasks[i].TaskID < tasks[j].TaskID
	})
	for _, task := range tasks {
		if len(out.Tasks) >= taskLimit {
			break
		}
		out.Tasks = append(out.Tasks, Task{
			TaskID:    task.TaskID,
			State:     task.State,
			Creator:   task.Creator,
			Assignee:  task.Assignee,
			CreatedAt: formatSeconds(task.CreatedAt),
			UpdatedAt: formatSeconds(task.UpdatedAt),
		})
	}

	sessions := append([]board.NamedSession(nil), raw.Sessions...)
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt != sessions[j].UpdatedAt {
			return sessions[i].UpdatedAt > sessions[j].UpdatedAt
		}
		return sessions[i].Name < sessions[j].Name
	})
	for _, session := range sessions {
		if len(out.Sessions) >= sessionLimit {
			break
		}
		out.Sessions = append(out.Sessions, Session{
			Name:      session.Name,
			Agent:     session.Agent,
			Mode:      session.Mode,
			Model:     session.Model,
			Closed:    session.Closed,
			CreatedAt: formatSeconds(session.CreatedAt),
			UpdatedAt: formatSeconds(session.UpdatedAt),
		})
	}

	var coordinator *board.CoordinatorLease
	if raw.Coordinator != nil {
		lease := *raw.Coordinator
		lease.ActiveAgents = append([]string(nil), raw.Coordinator.ActiveAgents...)
		coordinator = &lease
		out.Coordinator = &Coordinator{
			Status:       board.CoordinatorStatus(&lease, now),
			Leader:       lease.Leader,
			Term:         lease.Term,
			LeaseUntil:   formatSeconds(lease.LeaseUntil),
			UpdatedAt:    formatSeconds(lease.UpdatedAt),
			ActiveAgents: append([]string{}, lease.ActiveAgents...),
		}
	}

	for _, card := range cards {
		out.Agents = append(out.Agents, buildAgent(card, tasks, sessions, coordinator, now, staleAfter))
	}
	sort.Slice(out.Agents, func(i, j int) bool {
		left, right := agentOrder(out.Agents[i].State), agentOrder(out.Agents[j].State)
		if left != right {
			return left < right
		}
		return out.Agents[i].ID < out.Agents[j].ID
	})
	return out
}

func buildAgent(card agents.Card, tasks []board.Task, sessions []board.NamedSession, coordinator *board.CoordinatorLease, now time.Time, staleAfter time.Duration) Agent {
	out := Agent{
		ID:            card.ID,
		Name:          card.Name,
		Version:       card.Version,
		State:         "idle",
		IsCoordinator: coordinator != nil && coordinator.Leader == card.ID && board.CoordinatorStatus(coordinator, now) == "active",
	}
	var latest *board.Task
	var current *board.Task
	stateRank := 0
	for i := range tasks {
		task := &tasks[i]
		if task.Assignee != card.ID {
			continue
		}
		if latest == nil {
			latest = task
		}
		state := NormalizeState(task.State)
		age := now.Sub(secondsTime(task.UpdatedAt))
		rank := 0
		switch state {
		case "working":
			if age > staleAfter {
				state = "stale"
				rank = 3
			} else {
				rank = 4
			}
		case "failed":
			if age <= staleAfter {
				rank = 2
			}
		case "submitted", "input-required":
			rank = 1
		}
		if rank > stateRank {
			stateRank = rank
			current = task
		}
	}
	if latest != nil {
		out.LastActivityAt = formatSeconds(latest.UpdatedAt)
	}
	if current != nil {
		age := now.Sub(secondsTime(current.UpdatedAt))
		out.State = NormalizeState(current.State)
		if out.State == "working" && age > staleAfter {
			out.State = "stale"
		} else if out.State == "submitted" || out.State == "input-required" {
			out.State = "waiting"
		}
		out.CurrentTaskID = current.TaskID
		out.CurrentSession = sessionForTask(card.ID, current.SessionRef, sessions)
	}
	if out.CurrentSession == "" {
		for i := range sessions {
			if sessions[i].Agent == card.ID && !sessions[i].Closed {
				out.CurrentSession = sessions[i].Name
				break
			}
		}
	}
	if out.LastActivityAt == "" {
		for i := range sessions {
			if sessions[i].Agent != card.ID {
				continue
			}
			out.LastActivityAt = formatSeconds(sessions[i].UpdatedAt)
			break
		}
	}
	if coordinator != nil && coordinator.Leader == card.ID {
		out.LastCoordinatorAt = formatSeconds(coordinator.UpdatedAt)
	}
	return out
}

func sessionForTask(agentID, ref string, sessions []board.NamedSession) string {
	if ref == "" {
		return ""
	}
	for _, session := range sessions {
		if session.Agent != agentID {
			continue
		}
		if session.Name == ref || session.SessionID == ref {
			return session.Name
		}
	}
	return ""
}

func agentOrder(state string) int {
	switch state {
	case "working":
		return 0
	case "failed":
		return 1
	case "stale":
		return 2
	case "waiting":
		return 3
	default:
		return 4
	}
}

func formatSeconds(value float64) string {
	if value <= 0 {
		return ""
	}
	return secondsTime(value).UTC().Format(time.RFC3339Nano)
}

func secondsTime(value float64) time.Time {
	seconds := int64(value)
	nanos := int64((value - float64(seconds)) * 1e9)
	return time.Unix(seconds, nanos)
}

// NormalizeState keeps client-side labels predictable when a board has custom states.
func NormalizeState(state string) string {
	return strings.ToLower(strings.TrimSpace(state))
}
