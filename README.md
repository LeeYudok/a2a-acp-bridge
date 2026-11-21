# a2a-acp-bridge

`a2a-acp-bridge`는 `agy acp`와 `codex exec --json`을 잇는 Go 단일 바이너리다. 작업 위임과 상주 세션 풀, 공유 보드, MCP(모델 문맥 프로토콜) 서버를 제공한다.

외부 런타임(Node.js, Python)이나 서드파티 패키지 의존성이 없다. Go 1.22 이상 표준 라이브러리만으로 빌드한다. 폐쇄망 리눅스나 SSH 환경에 파일 하나만 복사하면 바로 동작한다. 2026-11-17 기준 `agy` 1.5.9와 `codex` 0.169.0에서 검증했다.

```sh
# 1. 빌드 및 설치
go build -o ~/.local/bin/a2a-acp-bridge .

# 2. AGY 및 Codex에 MCP 서버 등록
agy mcp add a2a-acp-bridge -- ~/.local/bin/a2a-acp-bridge mcp
codex mcp add a2a-acp-bridge -- ~/.local/bin/a2a-acp-bridge mcp

# 3. 명명 세션(-s)으로 AGY에 작업 위임 (상주 프로세스 자동 재사용)
a2a-acp-bridge ask-agy -s backend -mode auto "인증 미들웨어 에러 처리 구조 점검해줘"
```

---

## 1. 해결하는 문제와 핵심 기능

AI 코딩 에이전트 두 개(`agy`, `codex`)를 함께 쓸 때 프로세스를 매번 새로 띄우면 초기화 지연이 생긴다. 세션 ID(UUID)도 사람이 직접 관리해야 한다. `a2a-acp-bridge`는 `openclaw/acpx`의 세션 런타임 구조를 Go 단일 바이너리로 재설계해 아래 여섯 가지 기능을 제공한다.

| 기능 | 언제 쓰나 | 동작 원리 |
| :--- | :--- | :--- |
| 상주 ACP 세션 풀 (`SessionPool`) | 같은 세션에 연속으로 질문할 때 | 첫 턴에 띄운 `agy acp` 자식 프로세스를 종료하지 않고 메모리에 유지한다. 두 번째 턴부터 프로세스 생성과 초기화를 건너뛴다. |
| 스마트 세션 재연결 (`session/resume` · `session/load`) | 끊어진 세션을 다시 이어갈 때 | 에이전트가 `sessionCapabilities.resume`을 지원하면 히스토리 재전송 없이 즉시 복구한다. `session/load`로 폴백할 때는 과거 `session/update` 리플레이 청크를 자동으로 버린다. |
| 역방향 권한 요청 자동 응답 (`session/request_permission`) | 비대화형 위임 중 에이전트가 권한을 물을 때 | 에이전트가 `stdout`으로 보낸 역방향 JSON-RPC 요청(`id`와 `method` 동시 존재)을 감지해 즉시 승인 응답을 `stdin`으로 돌려준다. |
| 비선점 턴 취소 (`session/cancel`) | 긴 추론이나 잘못된 방향의 턴을 멈출 때 | 턴 실행 락(`turnMu`)과 표준입력 쓰기 락(`writeMu`)을 분리했다. 실행 도중에도 `session/cancel` 알림을 즉시 주입해 프로세스를 살려둔 채 현재 턴만 중단한다. |
| 런타임 모드·모델 전환 (`acp_session_control`) | 대화 도중 실행 모드나 모델을 바꿀 때 | 살아있는 세션에 `session/set_mode`, `session/set_config_option`, `session/set_model`, `session/close` RPC를 전송한다. |
| 명명 세션(`-s`) 및 비동기 큐(`-no-wait`) | UUID 대신 이름으로 세션을 관리하거나 백그라운드로 작업을 던질 때 | `-s backend` 같은 이름을 `sessionId`·`threadId`와 매핑해 보드 파일에 영속 저장한다. `-no-wait`(`async: true`)를 주면 백그라운드 고루틴에서 실행하고 즉시 `task_id`를 반환한다. |

---

## 2. 아키텍처와 통신 프로토콜

### 2.1 전체 구조

```
┌────────────────────────┐                  ┌────────────────────────┐
│   Antigravity (AGY)    │                  │    OpenAI Codex CLI    │
└───────────┬────────────┘                  └───────────┬────────────┘
            │ MCP (stdio JSON-RPC 2.0)                  │ MCP (stdio JSON-RPC 2.0)
            ▼                                           ▼
┌────────────────────────────────────────────────────────────────────┐
│                  a2a-acp-bridge (Go 단일 바이너리)                  │
│                                                                    │
│  ┌───────────────────────────┐    ┌─────────────────────────────┐  │
│  │   SessionPool (pool.go)   │    │     Board (board.go)        │  │
│  │  - acpWorker 상주 프로세스 │    │  - Tasks / Messages         │  │
│  │  - turnMu (턴 직렬화 큐)  │    │  - NamedSessions (-s 이름)   │  │
│  │  - writeMu (비선점 취소)  │    │  - 임시파일 + Rename 원자저장│  │
│  └─────────────┬─────────────┘    └─────────────────────────────┘  │
└────────────────┼──────────────────────────────────────────┬────────┘
                 │ ACP stdio (NDJSON / JSON-RPC 2.0)        │ JSONL 이벤트 스트림
                 ▼                                          ▼
   [ agy acp --dangerously-skip-permissions ]     [ codex exec / resume --json ]
```

### 2.2 ACP(Agent Client Protocol) 와이어 패킷 흐름

`acp_delegate_to_agy`를 호출하면 브리지는 `agy acp` 자식 프로세스와 OS 표준 입출력 파이프로 연결된다. 두 프로세스는 줄바꿈 구분 JSON-RPC 2.0(NDJSON)으로 통신한다.

1. **초기화 (`id: 1`, `initialize`)**
   브리지가 프로토콜 버전과 클라이언트 정보를 보낸다. 응답의 `agentCapabilities.sessionCapabilities`에서 `resume`과 `close` 지원 여부를 읽는다.
   ```json
   {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{},"clientInfo":{"name":"a2a-acp-bridge","version":"2.0.0"}}}
   ```
2. **세션 열기 (`id: 2`, `session/new` | `session/resume` | `session/load`)**
   새 세션이면 `session/new`를 호출해 `sessionId`를 발급받는다. 기존 세션 ID가 있으면 `session/resume`을 먼저 시도한다. 실패하면 `session/load`를 호출하고 로드 중 쏟아지는 과거 `session/update` 알림은 모두 버린다.
3. **프롬프트 실행 및 스트리밍 수집 (`id: 3`, `session/prompt`)**
   ```json
   {"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"sess-uuid","prompt":[{"type":"text","text":"요청 내용"}]}}
   ```
   - 에이전트가 작업하는 동안 `method: "session/update"` 알림이 스트리밍된다. 브리지는 `agent_message_chunk` 텍스트 조각과 `tool_call` 도구 이름을 누적한다.
   - 에이전트가 `method: "session/request_permission"` 요청을 보내면 `writeMu`를 잡고 즉시 승인 응답을 `stdin`에 기록한다.
   - `id: 3`에 대한 최종 응답(`stopReason: "end_turn"` 또는 `"cancelled"`)이 도착하면 누적한 텍스트와 도구 목록을 반환한다.
4. **세션 게이트 자동 통과**
   작업 디렉터리에 `.claude/state/personal-gate/current` 파일이 있으면 활성 사용자 이름을 읽는다. 첫 턴 응답이 사용자 확인을 요구하면 같은 세션 안에서 확인 프롬프트를 자동으로 보내 본답변을 받아온다.

---

## 3. 소스 파일 구조

모든 파일은 단일 책임 원칙에 따라 300줄 이하로 유지한다. 함수당 인지 복잡도(CC)도 15 이하로 구성했다(2026-11-22 기준 총 2,936줄).

| 파일 | 줄 수 | 역할 |
| :--- | :--- | :--- |
| `main.go` | 249 | CLI 엔트리포인트, `-timeout`·`-cwd` 파싱 및 서브커맨드 라우팅 |
| `board.go` | 268 | 공유 보드(`Board`) 자료구조, 에이전트 카드, 태스크·메시지 상태 갱신 |
| `board_sessions.go` | 238 | 수신함 조회, 태스크 조회, 명명 세션(`NamedSession`) 생성·조회·종료 |
| `board_store.go` | 95 | 파일 락 래퍼(`withLock`), 고유 임시파일(`os.CreateTemp`) 기반 원자적 저장 |
| `board_lock_unix.go` | 35 | Unix `flock` 기반 보드 다중 프로세스 배타 락 |
| `board_lock_other.go` | 7 | 비유닉스 환경 보드 파일 락 폴백 |
| `agy_history.go` | 64 | `agy-sessions` 서브커맨드 및 `session/list` 페이지 조회 |
| `clients.go` | 269 | ACP 스트림 파서, 역방향 권한 요청 자동 응답기, Codex JSONL 실행기 |
| `pool.go` | 294 | 상주 ACP 워커 풀(`SessionPool`), `PromptWithOptions`, 턴 취소·모드·모델 제어 |
| `pool_worker.go` | 295 | 개별 `acpWorker` 스폰, `session/set_mode`·`session/set_model` 선적용, 프롬프트 턴 실행 |
| `server.go` | 249 | MCP 도구 스키마 정의(`MCPTools`), `Dispatcher` 라우팅, 비동기 큐(`asyncWG`) |
| `server_transport.go` | 274 | Codex 위임 실행, `acp_session_control` 처리, MCP stdio JSON-RPC 서버 루프 |
| `server_http.go` | 71 | Go 1.22+ `http.ServeMux` 기반 A2A HTTP 서버 (`/.well-known/agent.json`, `/a2a`) |
| `bridge_test.go` | 282 | 모의 ACP 서버 기반 세션 풀 재사용·리플레이 억제·비동기 큐·MCP·HTTP 통합 테스트 |
| `bridge_improvements_test.go` | 246 | 다중 고루틴·멀티프로세스 보드 락, 모드·모델 선적용, CLI 플래그·`sync` intent 테스트 |

---

## 4. 설치 및 MCP 등록

### 4.1 빌드 및 테스트

```sh
go vet ./...
go test -race -v ./...
go build -o ~/.local/bin/a2a-acp-bridge .
```

### 4.2 로컬 MCP 등록

```sh
agy mcp add a2a-acp-bridge -- ~/.local/bin/a2a-acp-bridge mcp
codex mcp add a2a-acp-bridge -- ~/.local/bin/a2a-acp-bridge mcp
```

### 4.3 SSH 원격 호스트 MCP 등록

원격 서버에 `a2a-acp-bridge`와 `agy`·`codex`를 두면 로컬에서 SSH 표준 입출력으로 원격 에이전트 풀을 부를 수 있다.

```sh
cat << 'EOF' > ~/.local/bin/remote-a2a-mcp
#!/usr/bin/env bash
set -euo pipefail
exec ssh -q -o LogLevel=ERROR <원격호스트별칭> \
  'cd /path/to/remote/workspace && exec ~/.local/bin/a2a-acp-bridge mcp'
EOF
chmod +x ~/.local/bin/remote-a2a-mcp

agy mcp add remote-a2a-bridge -- ~/.local/bin/remote-a2a-mcp
codex mcp add remote-a2a-bridge -- ~/.local/bin/remote-a2a-mcp
```

MCP stdio 스트림이 깨지지 않도록 `ssh -q -o LogLevel=ERROR` 옵션을 준다. 그래야 SSH 배너나 호스트 키 경고가 표준출력에 섞이지 않는다.

---

## 5. MCP 도구 상세 명세

MCP 서버(`a2a-acp-bridge mcp`)는 아래 7개 도구를 노출한다(2026-11-22 기준).

| 도구 이름 | 언제 쓰나 | 필수 인자 | 선택 인자 |
| :--- | :--- | :--- | :--- |
| `a2a_discover_agents` | 사용 가능한 에이전트 카드, 열린 명명 세션, 최근 태스크 10건을 확인할 때 | 없음 | 없음 |
| `acp_delegate_to_agy` | AGY(`agy acp`)에게 코드 작성·분석·리팩터링을 맡길 때 | `prompt` | `session_name`, `session_id`, `mode`, `model`, `async`, `cwd` |
| `a2a_delegate_to_codex` | Codex(`codex exec`)에게 교차 코드 리뷰나 비대화형 작업을 맡길 때 | `prompt` | `session_name`, `thread_id`, `sandbox`, `async`, `cwd` |
| `acp_session_control` | 상주 세션 상태를 보거나 실행 중 취소·모드 변경·모델 변경·종료할 때 | `action` | `session`, `mode`, `model`, `cwd` |
| `a2a_send_message` | 에이전트 직접 실행 없이 공유 보드에 메시지·태스크만 남길 때 (`intent`: `delegate`/`review`/`reply`/`chat`/`sync`) | `from_agent`, `to_agent`, `content` | `intent`, `task_id` |
| `a2a_receive_messages` | 특정 에이전트(`agy` 또는 `codex`) 앞으로 온 메시지를 읽을 때 | `agent_id` | `unread_only`, `mark_read` |
| `a2a_task_status` | 특정 `task_id`의 진행 상태와 대화 내역을 조회하거나 상태를 바꿀 때 | 없음 | `task_id`, `state` |

### 5.1 `acp_delegate_to_agy` 호출 및 응답 예

요청 인자:
```json
{
  "prompt": "pool.go의 동시성 잠금 순서를 검토해줘",
  "session_name": "backend",
  "mode": "plan",
  "async": false
}
```

응답:
```json
{
  "agent": "agy",
  "protocol": "ACP-stdio-jsonrpc-2.0",
  "session_id": "6e8aa1fb-6401-4cfa-a547-84c86ca01b45",
  "session_name": "backend",
  "task_id": "task-d26f07b138",
  "response": "turnMu와 writeMu가 분리되어 있어 Cancel 호출 시 데드락이 발생하지 않습니다.",
  "tool_calls": ["read_file"],
  "stop_reason": "end_turn",
  "reused_process": true
}
```

### 5.2 `acp_session_control` 동작(`action`) 목록

- `status`: 현재 메모리에 살아있는 ACP 워커 목록(`pid`, `session_id`, `active_prompt`, `turn_count`)과 저장된 명명 세션 목록을 반환한다.
- `cancel`: `session`에 지정한 이름 또는 세션 ID의 실행 중인 프롬프트 턴에 `session/cancel`을 보낸다.
- `set_mode`: 세션 실행 모드를 `auto`, `accept-edits`, `plan` 중 하나로 바꾼다. `acp_delegate_to_agy`에 `mode`를 주면 해당 턴 프롬프트 전송 전에 먼저 적용한다.
- `set_model`: 세션 모델을 지정한 `model` ID로 바꾼다. `session/set_config_option`을 먼저 시도하고 실패하면 `session/set_model`로 폴백하며, `acp_delegate_to_agy`에 `model`을 주면 프롬프트 전송 전에 먼저 적용한다.
- `close`: `session/close` RPC를 보내고 워커 프로세스를 종료한 뒤 명명 세션을 닫힘(`closed: true`)으로 표시한다.

---

## 6. CLI 명령 레퍼런스

### 기존 AGY 대화에 연결

AGY를 시작한 디렉터리에서 `agy-sessions`를 실행한다. ACP의 `session/list` 응답에서 `sessionId`, 작업 디렉터리, 제목, 갱신 시각을 확인할 수 있다. 응답에 `nextCursor`가 있으면 `agy-sessions -cursor <nextCursor>`로 다음 목록을 조회한다.

```sh
a2a-acp-bridge agy-sessions -cwd /path/to/repo -timeout 3m
a2a-acp-bridge ask-agy -session-id <sessionId> -timeout 5m "진행 중인 작업과 검증 결과를 알려줘"
```

터미널이나 셸 스크립트에서 MCP 없이 직접 브리지를 제어할 수 있다.

```sh
# 1. 에이전트 카드, 활성 명명 세션, 최근 태스크 조회
a2a-acp-bridge cards

# 2. AGY에게 작업 위임 (-s 세션명, -mode 선적용 모드, -cwd/-timeout 지정)
a2a-acp-bridge ask-agy -s backend -mode auto -cwd /path/to/repo -timeout 5m "신규 엔드포인트 입력 검증 테스트 추가해줘"

# 3. AGY 비동기 큐 실행 (-no-wait 지정 시 즉시 task_id 반환)
a2a-acp-bridge ask-agy -s backend -no-wait "전체 패키지 정적 분석 실행해줘"

# 4. Codex에게 읽기 전용(read-only) 코드 리뷰 위임
a2a-acp-bridge ask-codex -s review -sandbox read-only -cwd /path/to/repo -timeout 5m "최근 커밋의 에러 래핑 누락 여부 확인해줘"

# 5. 세션 상태 조회 및 런타임 제어
a2a-acp-bridge sessions
a2a-acp-bridge set-mode -s backend -mode plan
a2a-acp-bridge set-model -s backend -model gemini-2.5-pro
a2a-acp-bridge cancel -s backend
a2a-acp-bridge close -s backend

# 6. 공유 보드 메시지 송신(delegate|review|reply|chat|sync) 및 수신함 확인
a2a-acp-bridge send -from agy -to codex -intent sync "머지 및 로컬 서버 기동 동기화"
a2a-acp-bridge inbox codex
a2a-acp-bridge inbox -all agy

# 7. A2A HTTP 서버 기동
a2a-acp-bridge serve-a2a -addr 127.0.0.1:8765
```

---

## 7. A2A HTTP 서버 API

`a2a-acp-bridge serve-a2a -addr 127.0.0.1:8765`로 기동하면 아래 HTTP 엔드포인트가 열린다. 느린 헤더 공격을 막기 위해 `ReadHeaderTimeout: 5s`와 요청 본문 `1 MiB` 상한이 기본 적용되어 있다.

| HTTP 메서드 및 경로 | 설명 |
| :--- | :--- |
| `GET /.well-known/agent.json` | A2A 표준 Agent Card, 명명 세션, 최근 태스크 JSON 반환 |
| `GET /agents` | `/.well-known/agent.json`과 동일 |
| `POST /a2a` | JSON-RPC 2.0 요청 본문을 받아 도구 실행 후 결과 반환 |

`POST /a2a`에서 지원하는 JSON-RPC `method` 매핑:

| JSON-RPC `method` | 연결되는 내부 도구 |
| :--- | :--- |
| `acp/agy` | `acp_delegate_to_agy` |
| `acp/control` | `acp_session_control` |
| `a2a/codex` | `a2a_delegate_to_codex` |
| `tasks/send` | `a2a_send_message` |
| `tasks/get` | `a2a_task_status` |

호출 예:
```sh
curl -s http://127.0.0.1:8765/a2a \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"acp/control","params":{"action":"status"}}'
```

---

## 8. 환경변수와 저장소 파일

| 환경변수 | 기본값 | 설명 |
| :--- | :--- | :--- |
| `A2A_BRIDGE_STORE` | `~/.local/share/a2a-acp-bridge/a2a_board.json` | 태스크·메시지·명명 세션을 저장하는 JSON 파일 경로 |
| `A2A_ACP_COMMAND` | `agy acp --dangerously-skip-permissions` | ACP 워커가 실행할 명령어와 인자 재정의 |
| `A2A_CODEX_COMMAND` | `codex` | Codex 위임 시 실행할 바이너리 명령 재정의 |

보드 저장소(`a2a_board.json`)는 프로세스 내부 뮤텍스와 Unix OS 파일 락(`a2a_board.json.lock`, `flock`)으로 다중 프로세스 동시 쓰기를 직렬화한다. 같은 디렉터리에 `os.CreateTemp`로 고유 임시 파일을 만들고 권한 `0600`으로 쓴 뒤 `os.Rename`으로 원자적 교체한다.
