package dispatch

import (
	"fmt"
	"strings"
	"time"

	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
)

func (d *Dispatcher) callCoordinatorLease(args map[string]any) (any, error) {
	action := strArg(args, "action", "status")
	agentID := strArg(args, "agent_id", "")
	if agentID == "" {
		agentID = d.caller(args)
	}
	ttl := board.DefaultLeaseTTL
	if sec := intArg(args, "ttl_seconds", 0); sec > 0 {
		ttl = time.Duration(sec) * time.Second
	}

	switch action {
	case "status":
		lease, err := d.Board.GetCoordinator()
		if err != nil {
			return nil, err
		}
		return map[string]any{"coordinator": lease}, nil
	case "claim":
		return d.Board.ClaimCoordinator(
			agentID,
			uint64Arg(args, "expected_term", 0),
			ttl,
			strArg(args, "reason", ""),
			strSliceArg(args, "active_agents"),
			boolArg(args, "force", false),
		)
	case "renew":
		return d.Board.RenewCoordinator(agentID, uint64Arg(args, "expected_term", 0), ttl)
	case "handoff":
		return d.Board.HandoffCoordinator(
			agentID,
			strArg(args, "to_agent", ""),
			uint64Arg(args, "expected_term", 0),
			strArg(args, "reason", ""),
			ttl,
		)
	default:
		return nil, fmt.Errorf("a2a_coordinator_lease: 알 수 없는 action %q (status|claim|renew|handoff)", action)
	}
}

func intArg(m map[string]any, k string, def int) int {
	v, ok := m[k]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case uint64:
		return int(n)
	case float64:
		return int(n)
	default:
		return def
	}
}

func uint64Arg(m map[string]any, k string, def uint64) uint64 {
	v, ok := m[k]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case uint64:
		return n
	case int:
		if n >= 0 {
			return uint64(n)
		}
	case int64:
		if n >= 0 {
			return uint64(n)
		}
	case float64:
		if n >= 0 {
			return uint64(n)
		}
	}
	return def
}

func strSliceArg(m map[string]any, k string) []string {
	v, ok := m[k]
	if !ok {
		return nil
	}
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok && strings.TrimSpace(str) != "" {
				out = append(out, strings.TrimSpace(str))
			}
		}
		return out
	case string:
		var out []string
		for _, part := range strings.Split(s, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	default:
		return nil
	}
}
