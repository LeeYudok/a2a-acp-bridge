package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
	"github.com/LeeYudok/a2a-acp-bridge/internal/dispatch"
)

const defaultWatchInterval = 1 * time.Second

type watchEvent struct {
	Type      string `json:"type"`
	Op        string `json:"op"`
	MessageID string `json:"message_id,omitempty"`
	FromAgent string `json:"from_agent,omitempty"`
	ToAgent   string `json:"to_agent,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	State     string `json:"state,omitempty"`
	Content   string `json:"content,omitempty"`
}

func runWatchCmd(ctx context.Context, d *dispatch.Dispatcher, rest []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	agent := fs.String("agent", "", "볼 에이전트 (agy|codex|claude|user, 기본: A2A_AGENT_ID 또는 user)")
	interval := fs.Duration("interval", defaultWatchInterval, "보드 폴링 주기")
	all := fs.Bool("all", false, "에이전트 필터 없이 전체 변화를 본다")
	timeout := fs.Duration("timeout", 0, "감시 지속 시간 (예: 3m, 10m). 0이면 종료 시그널까지 감시한다")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if *interval <= 0 {
		return fmt.Errorf("interval은 0보다 커야 합니다: %s", *interval)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("watch는 위치 인자를 받지 않습니다")
	}
	target := *agent
	if target == "" {
		target = d.Caller
	}
	if target == "" {
		target = "user"
	}
	watchCtx, cancel := ctx, func() {}
	if *timeout > 0 {
		watchCtx, cancel = context.WithTimeout(ctx, *timeout)
	}
	defer cancel()
	sigCtx, stop := signal.NotifyContext(watchCtx, os.Interrupt)
	defer stop()
	prev, err := d.Board.Snapshot()
	if err != nil {
		return err
	}
	printWatchEvent(watchEvent{Type: "watch", Op: "started", ToAgent: target})
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-sigCtx.Done():
			return nil
		case <-ticker.C:
			cur, err := d.Board.Snapshot()
			if err != nil {
				return err
			}
			for _, ev := range diffBoard(prev, cur, target, *all) {
				printWatchEvent(ev)
			}
			prev = cur
		}
	}
}

func printWatchEvent(ev watchEvent) {
	out, err := json.Marshal(ev)
	if err != nil {
		fmt.Printf("{\"type\":\"error\",\"op\":\"marshal\",\"content\":%q}\n", err.Error())
		return
	}
	fmt.Println(string(out))
}

// diffBoard 는 이전 스냅샷과 현재 스냅샷을 비교해 새 메시지·읽음·태스크 상태 변화를 이벤트로 돌려준다
func diffBoard(prev, cur board.Snapshot, agent string, all bool) []watchEvent {
	var out []watchEvent
	prevMsgs := map[string]board.Message{}
	for _, m := range prev.Messages {
		prevMsgs[m.MessageID] = m
	}
	for _, m := range cur.Messages {
		old, seen := prevMsgs[m.MessageID]
		relevant := all || m.ToAgent == agent || m.FromAgent == agent
		if !relevant {
			continue
		}
		switch {
		case !seen:
			out = append(out, watchEvent{
				Type: "message", Op: "new", MessageID: m.MessageID,
				FromAgent: m.FromAgent, ToAgent: m.ToAgent,
				TaskID: m.TaskID, Content: m.Content,
			})
		case m.ReadAt > 0 && old.ReadAt == 0:
			out = append(out, watchEvent{
				Type: "message", Op: "read", MessageID: m.MessageID,
				FromAgent: m.FromAgent, ToAgent: m.ToAgent, TaskID: m.TaskID,
			})
		}
	}
	prevTasks := map[string]board.Task{}
	for _, t := range prev.Tasks {
		prevTasks[t.TaskID] = t
	}
	for _, t := range cur.Tasks {
		old, seen := prevTasks[t.TaskID]
		relevant := all || t.Creator == agent || t.Assignee == agent
		if !relevant || (seen && old.State == t.State) {
			continue
		}
		out = append(out, watchEvent{
			Type: "task", Op: "state", TaskID: t.TaskID,
			FromAgent: t.Creator, ToAgent: t.Assignee, State: t.State, Content: t.Title,
		})
	}
	return out
}
