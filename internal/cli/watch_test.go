package cli

import (
	"testing"

	"github.com/LeeYudok/a2a-acp-bridge/internal/board"
)

func TestDiffBoardEventsForNewAndReadMessages(t *testing.T) {
	prev := board.Snapshot{}
	cur := board.Snapshot{Messages: []board.Message{
		{MessageID: "m1", FromAgent: "agy", ToAgent: "codex", Content: "위임"},
	}}
	evs := diffBoard(prev, cur, "codex", false)
	if len(evs) != 1 || evs[0].Type != "message" || evs[0].Op != "new" || evs[0].MessageID != "m1" {
		t.Fatalf("새 메시지 이벤트 불일치: %+v", evs)
	}
	cur.Messages[0].ReadAt = 2
	evs = diffBoard(cur, cur, "codex", false)
	if len(evs) != 0 {
		t.Fatalf("읽음 변화가 없는데 이벤트가 남았다: %+v", evs)
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
