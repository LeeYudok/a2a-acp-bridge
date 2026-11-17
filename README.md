# a2a-acp-bridge

Google Antigravity CLI (`agy acp`)와 OpenAI Codex CLI (`codex exec --json`) 간 양방향 작업 위임·상주 ACP 세션 풀·공유 태스크/메시지 보드·MCP 서버를 제공하는 **순수 Go 표준 라이브러리 단일 바이너리** 브리지입니다.

외부 런타임(Node.js, Python 등)이나 서드파티 의존성 없이 단일 정적/표준 바이너리로 동작하며, 폐쇄망(Air-gapped) 리눅스 환경과 원격 SSH 연동 환경에서도 파일 하나만 복사해 즉시 사용할 수 있습니다.

---

## 주요 기능 (v2.0 — `openclaw/acpx` 아키텍처 흡수)

1. **상주 ACP 세션 풀 (Warm Keep-Alive Session Pool)**
   - 매 프롬프트마다 `agy acp` 프로세스를 새로 띄우지 않고, 세션별 워커 프로세스를 메모리에 상주시켜 2번째 턴부터 초기화(`initialize` + `session/load`) 지연을 제거합니다.
   - 워커 프로세스 비정상 종료 시 자동으로 재기동 및 세션을 복구(Self-Healing)합니다.
2. **스마트 세션 재연결 (`session/resume` 우선 + `session/load` 리플레이 노이즈 억제)**
   - 에이전트가 `sessionCapabilities.resume`을 지원하면 무거운 히스토리 재전송 없이 즉시 세션을 이어받습니다.
   - `session/load` 폴백 시에는 로드 과정에서 쏟아지는 과거 `session/update` 리플레이 청크를 자동 드레인(Suppress)해 현재 턴 응답이 오염되지 않습니다.
3. **양방향 Agent-to-Client RPC 자동 처리 (`session/request_permission`)**
   - 프롬프트 도중 에이전트가 클라이언트로 역방향 JSON-RPC 권한 요청(`session/request_permission`)을 보내면 비대화형 승인 응답을 즉시 반환해 데드락을 방지합니다.
4. **비선점 턴 취소 (`session/cancel`) 및 런타임 모드·모델 제어**
   - `turnMu`(턴 직렬화 큐)와 `writeMu`(프레임 송신 락)를 분리해, 긴 추론 도중에도 `session/cancel` 알림을 즉시 주입하여 프로세스를 죽이지 않고 현재 턴만 우아하게 중단합니다.
   - 실행 중인 세션의 모드(`session/set_mode`: `auto` | `accept-edits` | `plan`)와 모델(`session/set_config_option` / `session/set_model`)을 동적으로 전환합니다.
5. **명명 세션 (`-s <name>` / `session_name`) 및 비동기 큐 (`-no-wait` / `async: true`)**
   - UUID를 외울 필요 없이 `-s backend`, `-s review` 같은 논리적 세션 이름으로 AGY(`sessionId`)와 Codex(`threadId`) 대화를 영속 관리합니다.
   - `async: true` (`-no-wait`) 지정 시 작업을 백그라운드 큐에 넣고 즉시 `task_id` (`state: "working"`)를 반환합니다.
6. **MCP 서버 (`stdio`) & A2A HTTP 서버 (`/.well-known/agent.json`, `/a2a`) 내장**
   - AGY와 Codex 양쪽에 MCP 서버로 등록해 에이전트가 서로를 네이티브 도구로 호출할 수 있습니다.

---

## 아키텍처

```
┌──────────────────────┐                  ┌──────────────────────┐
│  Antigravity (AGY)   │                  │   OpenAI Codex CLI   │
└──────────┬───────────┘                  └──────────┬───────────┘
           │ MCP (stdio JSON-RPC 2.0)                │ MCP (stdio JSON-RPC 2.0)
           ▼                                         ▼
┌────────────────────────────────────────────────────────────────┐
│                 a2a-acp-bridge (Go Single Binary)              │
│                                                                │
│  ┌─────────────────────────┐    ┌───────────────────────────┐  │
│  │   Warm ACP SessionPool  │    │  A2A Shared Board Store   │  │
│  │  - Keep-Alive Workers   │    │  - Tasks & Messages       │  │
│  │  - session/resume & load│    │  - Named Sessions (-s)    │  │
│  │  - session/cancel       │    │  - Atomic JSON Persistence│  │
│  └────────────┬────────────┘    └───────────────────────────┘  │
└───────────────┼────────────────────────────────────────┬───────┘
                │ ACP stdio (NDJSON)                     │ JSONL stream
                ▼                                        ▼
      [ agy acp (pooled) ]                    [ codex exec / resume ]
```

---

## 빌드 및 테스트

Go 1.22 이상 표준 툴체인만 있으면 빌드됩니다.

```sh
go vet ./...
go test -race -v ./...
go build -o ~/.local/bin/a2a-acp-bridge .
```

---

## MCP 서버 등록

### 로컬 등록
```sh
agy mcp add a2a-acp-bridge -- ~/.local/bin/a2a-acp-bridge mcp
codex mcp add a2a-acp-bridge -- ~/.local/bin/a2a-acp-bridge mcp
```

### 원격 서버(SSH) 브리지 등록 예시
원격 호스트의 에이전트 풀을 로컬 MCP 도구로 연결할 수 있습니다:
```sh
#!/usr/bin/env bash
exec ssh -q -o LogLevel=ERROR <remote-host> 'cd /path/to/workspace && exec ~/.local/bin/a2a-acp-bridge mcp'
```

---

## 제공하는 MCP 도구

| 도구명 | 설명 |
| :--- | :--- |
| `a2a_discover_agents` | `agy`·`codex` A2A Agent Card, 활성 명명 세션(`sessions`), 최근 태스크 목록 조회 |
| `acp_delegate_to_agy` | 상주 ACP 세션 풀을 통해 AGY에 작업 위임 (`prompt`, `session_name`, `session_id`, `mode`, `model`, `async`) |
| `a2a_delegate_to_codex` | Codex CLI에 작업·코드 리뷰 위임 (`prompt`, `session_name`, `thread_id`, `sandbox`, `async`) |
| `acp_session_control` | ACP 세션 런타임 제어 (`action`: `status` \| `cancel` \| `set_mode` \| `set_model` \| `close`) |
| `a2a_send_message` | 공유 A2A 보드에 에이전트 간 메시지(`delegate`·`review`·`reply`·`chat`) 기록 |
| `a2a_receive_messages` | 에이전트별 미열람/전체 수신함 조회 및 읽음 처리 |
| `a2a_task_status` | 공유 태스크(`submitted`·`working`·`completed`·`failed`) 상세 조회 및 상태 갱신 |

---

## CLI 사용법

```sh
# 에이전트 카드 및 최근 태스크/세션 조회
a2a-acp-bridge cards

# 명명 세션(-s)으로 AGY에게 작업 위임 (상주 프로세스 자동 재사용)
a2a-acp-bridge ask-agy -s backend -mode auto "인증 미들웨어 리팩터링 방안 검토해줘"

# 비동기(--no-wait) 큐잉으로 작업 던지기
a2a-acp-bridge ask-agy -s backend -no-wait "전체 패키지 테스트케이스 보강해줘"

# 명명 세션으로 Codex에 교차 리뷰 위임
a2a-acp-bridge ask-codex -s review -sandbox read-only "최근 변경사항 보안 이슈 점검해줘"

# 활성 세션/워커 상태 확인, 모드·모델 변경, 실행 중 턴 취소, 세션 종료
a2a-acp-bridge sessions
a2a-acp-bridge set-mode -s backend -mode plan
a2a-acp-bridge set-model -s backend -model gemini-2.5-pro
a2a-acp-bridge cancel -s backend
a2a-acp-bridge close -s backend

# 공유 보드 메시지 송수신
a2a-acp-bridge send -from agy -to codex -intent review "PR #12 리뷰 요청"
a2a-acp-bridge inbox codex

# A2A HTTP 서버 기동 (/.well-known/agent.json, POST /a2a)
a2a-acp-bridge serve-a2a -addr 127.0.0.1:8765
```

---

## 환경변수

| 변수명 | 설명 | 기본값 |
| :--- | :--- | :--- |
| `A2A_BRIDGE_STORE` | 공유 보드 JSON 파일 경로 | `~/.local/share/a2a-acp-bridge/a2a_board.json` |
| `A2A_ACP_COMMAND` | 실행할 ACP 서버 명령 재정의 | `agy acp --dangerously-skip-permissions` |
| `A2A_CODEX_COMMAND` | 실행할 Codex CLI 명령 재정의 | `codex` |
