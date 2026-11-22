package dispatch

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/LeeYudok/a2a-acp-bridge/internal/agents"
	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
)

func poolTool() ToolSchema {
	return ToolSchema{
		Name: "a2a_pool", Description: "계열별 위임 풀 상태 조회 및 상한·하한 변경",
		InputSchema: objectSchema(map[string]any{
			"action":    enumProp([]string{"status", "set"}),
			"family":    strProp("변경할 계열 ID"),
			"min":       map[string]any{"type": "integer", "minimum": 0},
			"max":       map[string]any{"type": "integer", "minimum": 1},
			"max_total": map[string]any{"type": "integer", "minimum": 1},
		}, "action"),
	}
}

// CallPool uses a transport-assigned identity for settings writes.
func (d *Dispatcher) CallPool(args map[string]any, actor string) (board.PoolStatus, error) {
	families := agents.Families(agents.Cards())
	switch action := strArg(args, "action", ""); action {
	case "status":
		return d.Board.PoolStatus(families)
	case "set":
		return d.setPool(args, actor, families)
	default:
		return board.PoolStatus{}, fmt.Errorf("a2a_pool: 잘못된 action %q (status|set)", action)
	}
}

func (d *Dispatcher) setPool(args map[string]any, actor string, families map[string]string) (board.PoolStatus, error) {
	if _, ok := args["max_total"]; ok {
		if _, family := args["family"]; family || hasBounds(args) {
			return board.PoolStatus{}, fmt.Errorf("pool: max_total 은 단독으로 지정해야 합니다")
		}
		limit, err := poolInt(args["max_total"], "max_total")
		if err != nil {
			return board.PoolStatus{}, err
		}
		return d.Board.SetPool(actor, "", nil, &limit, families)
	}
	family := strArg(args, "family", "")
	if !agents.IsValidSlug(family) {
		return board.PoolStatus{}, fmt.Errorf("pool: 잘못된 family %q", family)
	}
	minimum, err := poolInt(args["min"], "min")
	if err != nil {
		return board.PoolStatus{}, err
	}
	maximum, err := poolInt(args["max"], "max")
	if err != nil {
		return board.PoolStatus{}, err
	}
	return d.Board.SetPool(actor, family, &board.PoolBounds{Min: minimum, Max: maximum}, nil, families)
}

func hasBounds(args map[string]any) bool {
	_, hasMin := args["min"]
	_, hasMax := args["max"]
	return hasMin || hasMax
}

func poolInt(value any, key string) (int, error) {
	switch n := value.(type) {
	case int:
		return n, nil
	case json.Number:
		if v, err := strconv.Atoi(n.String()); err == nil {
			return v, nil
		}
	case float64:
		if math.Trunc(n) == n && n >= 0 && n < maxExactFloat53 && n <= float64(math.MaxInt) {
			return int(n), nil
		}
	}
	return 0, fmt.Errorf("pool: %s 에 범위 내 정수를 지정해야 합니다", key)
}

func (d *Dispatcher) startDelegation(from, to, prompt, session string) (board.Message, error) {
	return d.Board.StartDelegation(from, to, prompt, session, agents.Families(agents.Cards()))
}
