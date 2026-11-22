package cli

import (
	"testing"

	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
)

func TestDiffBoardEventsForNewAndReadMessages(t *testing.T) {
	prev := board.Snapshot{}
	cur := board.Snapshot{Messages: []board.Message{
		{MessageID: "m1", FromAgent: "agy", ToAgent: "codex", Content: "위임", ReadAt: 2},
	}}
	evs := diffBoard(prev, cur, "codex", false)
	if len(evs) != 2 || evs[0].Op != "new" || evs[1].Op != "read" || evs[0].MessageID != "m1" || evs[1].MessageID != "m1" {
		t.Fatalf("같은 주기의 새 메시지·읽음 이벤트 불일치: %+v", evs)
	}
	evs = diffBoard(cur, cur, "codex", false)
	if len(evs) != 0 {
		t.Fatalf("읽음 변화가 없는데 이벤트가 남았다: %+v", evs)
	}
}

func TestDiffBoardEmitsReadForSeenMessage(t *testing.T) {
	prev := board.Snapshot{Messages: []board.Message{
		{MessageID: "m1", FromAgent: "agy", ToAgent: "codex", ReadAt: 0},
	}}
	cur := board.Snapshot{Messages: []board.Message{
		{MessageID: "m1", FromAgent: "agy", ToAgent: "codex", ReadAt: 2},
	}}
	evs := diffBoard(prev, cur, "codex", false)
	if len(evs) != 1 || evs[0].Type != "message" || evs[0].Op != "read" {
		t.Fatalf("기존 메시지 읽음 이벤트 불일치: %+v", evs)
	}
}

func TestDiffBoardRespectsAgentFilterAndTaskState(t *testing.T) {
	prev := board.Snapshot{Tasks: []board.Task{{TaskID: "t1", Creator: "codex", Assignee: "agy", State: "submitted"}}}
	cur := board.Snapshot{
		Tasks: []board.Task{{TaskID: "t1", Creator: "codex", Assignee: "agy", State: "completed"}},
		Messages: []board.Message{
			{MessageID: "m2", FromAgent: "agy", ToAgent: "claude", Content: "다른 대상"},
			{MessageID: "m3", FromAgent: "codex", ToAgent: "agy", Content: "나에게 온 것"},
		},
	}
	evs := diffBoard(prev, cur, "claude", false)
	var got []string
	for _, ev := range evs {
		got = append(got, ev.Type+"/"+ev.Op)
	}
	if len(evs) != 1 || got[0] != "message/new" || evs[0].MessageID != "m2" || evs[0].ToAgent != "claude" {
		t.Fatalf("claude 필터는 m2 만 봐야 한다: %+v", evs)
	}
	all := diffBoard(prev, cur, "user", true)
	agy := diffBoard(prev, cur, "agy", false)
	if len(all) != 3 || len(agy) != 3 {
		t.Fatalf("-all 은 3 이벤트, agy 관련은 3 이벤트(m2·m3·t1)다: all=%+v agy=%+v", all, agy)
	}
	if agy[0].MessageID != "m2" || agy[1].MessageID != "m3" || agy[2].TaskID != "t1" || agy[2].State != "completed" {
		t.Fatalf("agy 관련 이벤트 순서·내용 불일치: %+v", agy)
	}
}

func TestDiffBoardEmitsCoordinatorState(t *testing.T) {
	prev := board.Snapshot{Coordinator: &board.CoordinatorLease{
		Leader: "agy", Term: 1, LeaseUntil: 100, ActiveAgents: []string{"agy", "codex"},
	}}
	cur := board.Snapshot{Coordinator: &board.CoordinatorLease{
		Leader: "codex", Term: 2, LeaseUntil: 200, PreviousLeader: "agy", Reason: "agy 응답 없음",
		ActiveAgents: []string{"agy", "codex", "claude"},
	}}
	evs := diffBoard(prev, cur, "codex", false)
	if len(evs) != 1 || evs[0].Type != "coordinator" || evs[0].Op != "state" {
		t.Fatalf("coordinator 이벤트 불일치: %+v", evs)
	}
	ev := evs[0]
	if ev.FromAgent != "agy" || ev.ToAgent != "codex" || ev.Coordinator == nil || ev.Coordinator.Term != 2 || ev.Content != "agy 응답 없음" {
		t.Fatalf("coordinator 이벤트 내용 불일치: %+v", ev)
	}
	if evs := diffBoard(prev, cur, "claude", false); len(evs) != 1 {
		t.Fatalf("active agent 는 coordinator 변화를 받아야 한다: %+v", evs)
	}
	if evs := diffBoard(prev, cur, "user", false); len(evs) != 0 {
		t.Fatalf("관련 없는 agent 에게 coordinator 변화가 노출됨: %+v", evs)
	}
}
