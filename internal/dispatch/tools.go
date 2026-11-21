package dispatch

import "github.com/LeeYudok/a2a-acp-bridge/internal/agents"

// ToolSchema 는 MCP tools/list 항목이다
type ToolSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

var (
	modeEnum   = []string{"auto", "accept-edits", "plan"}
	intentEnum = []string{"delegate", "review", "reply", "chat", "sync"}
	stateEnum  = []string{"submitted", "working", "input-required", "completed", "failed"}
)

// fromProp 은 위임 도구의 발신자 인자다. 생략하면 Dispatcher.Caller(없으면 user)로 기록한다
func fromProp() map[string]any {
	return map[string]any{"type": "string", "enum": agents.Senders(), "description": "보드에 기록할 발신자 (선택, 기본: 호출한 에이전트 또는 user)"}
}

func objectSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func enumProp(values []string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

// Tools 는 Dispatcher.CallTool 이 처리하는 도구의 스키마 목록이다
func Tools() []ToolSchema {
	return []ToolSchema{
		{
			Name:        "a2a_discover_agents",
			Description: "A2A Agent Card 목록(agy, codex, claude), 등록된 명명 세션(sessions), 최근 공유 태스크 현황을 조회합니다.",
			InputSchema: objectSchema(map[string]any{}),
		},
		{
			Name:        "acp_delegate_to_agy",
			Description: "ACP(상주 세션 풀·자동 재연결)를 통해 AGY에게 작업을 위임합니다. session_name(-s)과 async(--no-wait) 큐잉을 지원합니다.",
			InputSchema: objectSchema(map[string]any{
				"prompt":       strProp("AGY에게 전달할 프롬프트/작업 지시"),
				"session_id":   strProp("이어갈 ACP 세션 ID (선택)"),
				"session_name": strProp("acpx 스타일의 논리 세션 이름 (예: backend, docs)"),
				"mode":         enumProp(modeEnum),
				"model":        strProp("세션에 적용할 모델 ID (선택)"),
				"async":        map[string]any{"type": "boolean", "description": "true면 백그라운드 큐에 넣고 즉시 task_id 반환(--no-wait)"},
				"cwd":          strProp("작업 디렉터리 (선택)"),
				"from_agent":   fromProp(),
			}, "prompt"),
		},
		{
			Name:        "a2a_delegate_to_codex",
			Description: "A2A 브리지를 통해 Codex(`codex exec --json`)에게 작업/리뷰를 위임합니다. session_name과 async 큐잉을 지원합니다.",
			InputSchema: objectSchema(map[string]any{
				"prompt":       strProp("Codex에게 전달할 프롬프트/작업 지시"),
				"thread_id":    strProp("이어갈 Codex 스레드 ID (선택)"),
				"session_name": strProp("acpx 스타일의 논리 세션 이름 (선택)"),
				"sandbox":      enumProp([]string{"read-only", "workspace-write"}),
				"async":        map[string]any{"type": "boolean", "description": "true면 백그라운드 큐에 넣고 즉시 task_id 반환"},
				"cwd":          strProp("작업 디렉터리 (선택)"),
				"from_agent":   fromProp(),
			}, "prompt"),
		},
		{
			Name:        "acp_session_control",
			Description: "acpx 스타일의 ACP 세션 런타임 제어(status | cancel | set_mode | set_model | close)를 수행합니다.",
			InputSchema: objectSchema(map[string]any{
				"action":  enumProp([]string{"status", "cancel", "set_mode", "set_model", "close"}),
				"session": strProp("대상 세션 이름(session_name) 또는 session_id"),
				"mode":    enumProp(modeEnum),
				"model":   strProp("변경할 모델 ID (set_model 시)"),
				"cwd":     strProp("작업 디렉터리 (선택)"),
			}, "action"),
		},
		{
			Name:        "a2a_send_message",
			Description: "공유 A2A 메시지 보드에 다른 에이전트(agy, codex, claude) 앞으로 메시지/태스크를 남깁니다.",
			InputSchema: objectSchema(map[string]any{
				"from_agent": fromProp(),
				"to_agent":   enumProp(agents.IDs()),
				"content":    strProp("전달할 메시지 본문"),
				"intent":     enumProp(intentEnum),
				"task_id":    strProp("연결할 태스크 ID (선택)"),
			}, "to_agent", "content"),
		},
		{
			Name:        "a2a_receive_messages",
			Description: "공유 A2A 메시지 보드에서 특정 에이전트(agy, codex, claude) 앞으로 도착한 메시지를 읽습니다.",
			InputSchema: objectSchema(map[string]any{
				"agent_id":    enumProp(agents.Senders()),
				"unread_only": map[string]any{"type": "boolean"},
				"mark_read":   map[string]any{"type": "boolean"},
			}, "agent_id"),
		},
		{
			Name:        "a2a_task_status",
			Description: "공유 A2A 태스크 상세 조회 또는 상태(submitted/working/completed/failed) 갱신을 수행합니다.",
			InputSchema: objectSchema(map[string]any{
				"task_id": map[string]any{"type": "string"},
				"state":   enumProp(stateEnum),
			}),
		},
	}
}
