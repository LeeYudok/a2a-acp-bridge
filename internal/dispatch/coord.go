package dispatch

import (
	"fmt"
	"math"
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
	ttl, err := parseLeaseTTL(args, action)
	if err != nil {
		return nil, err
	}

	switch action {
	case "status":
		lease, err := d.Board.GetCoordinator()
		if err != nil {
			return nil, err
		}
		return map[string]any{"coordinator": lease}, nil
	case "claim":
		expectedTerm, err := parseExpectedTerm(args)
		if err != nil {
			return nil, err
		}
		return d.Board.ClaimCoordinator(
			agentID,
			expectedTerm,
			ttl,
			strArg(args, "reason", ""),
			strSliceArg(args, "active_agents"),
			boolArg(args, "force", false),
		)
	case "renew":
		expectedTerm, err := parseExpectedTerm(args)
		if err != nil {
			return nil, err
		}
		return d.Board.RenewCoordinator(agentID, expectedTerm, ttl)
	case "handoff":
		expectedTerm, err := parseExpectedTerm(args)
		if err != nil {
			return nil, err
		}
		return d.Board.HandoffCoordinator(
			agentID,
			strArg(args, "to_agent", ""),
			expectedTerm,
			strArg(args, "reason", ""),
			ttl,
		)
	default:
		return nil, fmt.Errorf("a2a_coordinator_lease: 알 수 없는 action %q (status|claim|renew|handoff)", action)
	}
}

func parseExpectedTerm(args map[string]any) (uint64, error) {
	raw, ok := args["expected_term"]
	if !ok || raw == nil {
		return 0, fmt.Errorf("a2a_coordinator_lease: expected_term 인자가 필요합니다")
	}
	switch n := raw.(type) {
	case uint64:
		return n, nil
	case int:
		if n < 0 {
			return 0, fmt.Errorf("a2a_coordinator_lease: expected_term 은 음이 아닌 정수여야 합니다: %d", n)
		}
		return uint64(n), nil
	case int64:
		if n < 0 {
			return 0, fmt.Errorf("a2a_coordinator_lease: expected_term 은 음이 아닌 정수여야 합니다: %d", n)
		}
		return uint64(n), nil
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || math.Trunc(n) != n {
			return 0, fmt.Errorf("a2a_coordinator_lease: expected_term 은 음이 아닌 정수여야 합니다: %v", n)
		}
		return uint64(n), nil
	default:
		return 0, fmt.Errorf("a2a_coordinator_lease: expected_term 형식이 잘못되었습니다(%T)", raw)
	}
}

func parseLeaseTTL(args map[string]any, action string) (time.Duration, error) {
	raw, ok := args["ttl_seconds"]
	if !ok || raw == nil {
		return board.DefaultLeaseTTL, nil
	}
	var sec int64
	switch n := raw.(type) {
	case int:
		sec = int64(n)
	case int64:
		sec = n
	case uint64:
		sec = int64(n)
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n {
			return 0, fmt.Errorf("a2a_coordinator_lease: ttl_seconds 는 정수여야 합니다: %v", n)
		}
		sec = int64(n)
	default:
		return 0, fmt.Errorf("a2a_coordinator_lease: ttl_seconds 형식이 잘못되었습니다(%T)", raw)
	}
	if sec < 0 || (sec == 0 && (action == "claim" || action == "renew")) {
		if sec == 0 && args["ttl_seconds"] != nil {
			// CLI coord command may pass 0 for handoff; keep claim/renew default when 0 unless negative.
			if sec < 0 {
				return 0, fmt.Errorf("a2a_coordinator_lease: ttl_seconds 는 음수일 수 없습니다: %d", sec)
			}
			return board.DefaultLeaseTTL, nil
		}
		return 0, fmt.Errorf("a2a_coordinator_lease: ttl_seconds 는 양의 정수여야 합니다: %d", sec)
	}
	if sec == 0 {
		return board.DefaultLeaseTTL, nil
	}
	return time.Duration(sec) * time.Second, nil
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
