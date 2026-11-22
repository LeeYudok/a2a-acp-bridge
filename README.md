# a2a-acp-bridge

`a2a-acp-bridge`는 `agy`(Antigravity), `codex`(Codex CLI), `claude`(Claude Code) 세 에이전트가 서로에게 작업을 맡기고 결과를 주고받게 하는 Go 단일 바이너리다. AGY는 `agy acp`(ACP, 상주 세션 풀), Codex는 `codex exec --json`, Claude Code는 `claude -p --output-format json`으로 부른다. 세 에이전트 모두 호출자이자 위임 대상이다. 위임 기록은 공유 보드에 남고, MCP(모델 문맥 프로토콜) 서버·A2A HTTP 서버·CLI로 쓴다.

외부 런타임(Node.js, Python)이나 서드파티 패키지 의존성이 없다. Go 1.22 이상 표준 라이브러리만으로 빌드한다. 폐쇄망 리눅스나 SSH 환경에 파일 하나만 복사하면 바로 동작한다. 2026-11-22 기준 설치본 `agy` 1.5.14, `codex-cli` 0.170.0, Claude Code 2.1.330에서 확인했다. AGY는 PR #6의 테스트와 실사용으로, Codex는 실제 `ask-codex` 호출로, Claude Code는 실제 `ask-claude` 왕복으로 확인했다.

```sh
# 1. 빌드 및 설치
go build -o ~/.local/bin/a2a-acp-bridge ./cmd/a2a-acp-bridge

# 2. AGY, Codex, Claude Code에 MCP 서버 등록
agy mcp add a2a-acp-bridge -- ~/.local/bin/a2a-acp-bridge mcp
codex mcp add a2a-acp-bridge -- ~/.local/bin/a2a-acp-bridge mcp
claude mcp add a2a-acp-bridge -- ~/.local/bin/a2a-acp-bridge mcp

# 3. 명명 세션(-s)으로 AGY에 작업 위임 (상주 프로세스 자동 재사용)
a2a-acp-bridge ask-agy -s backend -mode auto "인증 미들웨어 에러 처리 구조 점검해줘"

# 4. Claude Code에 읽기 전용 리뷰 위임 (기본 권한 모드 plan)
a2a-acp-bridge ask-claude -s review "최근 커밋의 에러 래핑 누락 여부 확인해줘"
```

---

## 1. 해결하는 문제와 핵심 기능

AI 코딩 에이전트 셋(`agy`, `codex`, `claude`)을 함께 쓸 때 프로세스를 매번 새로 띄우면 초기화 지연이 생긴다. 세션 ID(UUID)도 사람이 직접 관리해야 한다. 누가 누구에게 무엇을 맡겼는지도 흩어진다. `a2a-acp-bridge`는 `openclaw/acpx`의 세션 런타임 구조를 Go 단일 바이너리로 재설계해 아래 아홉 가지 기능을 제공한다.

| 기능 | 언제 쓰나 | 동작 원리 |
| :--- | :--- | :--- |
| 세 에이전트 위임 | 어느 에이전트에서든 다른 에이전트에 작업이나 리뷰를 맡길 때 | AGY는 ACP stdio, Codex는 `codex exec --json`, Claude Code는 `claude -p --output-format json`으로 실행한다. MCP 도구(`acp_delegate_to_agy`·`a2a_delegate_to_codex`·`a2a_delegate_to_claude`), CLI(`ask-agy`·`ask-codex`·`ask-claude`), HTTP(`acp/agy`·`a2a/codex`·`a2a/claude`)가 같은 `Dispatcher` 하나를 거친다. |
| Claude Code 위임 | Claude Code에 교차 리뷰나 분석을 맡길 때 | 기본 권한 모드는 `plan`(읽기 전용)이다. `permission_mode`로 `default`·`acceptEdits`를 고른다. `session_id` 또는 명명 세션으로 `--resume` 이어가기를 한다. 응답에 `cost_usd`가 들어 있다. 실제 왕복 한 번에 약 $0.34가 들었다(프롬프트 캐시 생성). |
| 발신자 기록과 답신 수신함 | 위임 결과를 호출한 에이전트의 수신함으로 받을 때 | `from_agent` 인자, CLI `-from`, 환경변수 `A2A_AGENT_ID`, MCP `initialize`의 `clientInfo.name` 추정 순으로 발신자를 정한다. 모두 없으면 `user`다. 대상 에이전트의 응답은 `reply` 메시지로 그 발신자의 수신함에 쌓인다. |
| 상주 ACP 세션 풀 (`SessionPool`, AGY) | 같은 AGY 세션에 연속으로 질문할 때 | 첫 턴에 띄운 `agy acp` 자식 프로세스를 종료하지 않고 메모리에 유지한다. 두 번째 턴부터 프로세스 생성과 초기화를 건너뛴다. |
| 스마트 세션 재연결 (`session/resume` · `session/load`, AGY) | 끊어진 세션을 다시 이어갈 때 | 에이전트가 `sessionCapabilities.resume`을 지원하면 히스토리 재전송 없이 즉시 복구한다. `session/load`로 폴백할 때는 과거 `session/update` 리플레이 청크를 자동으로 버린다. |
| 역방향 권한 요청 자동 응답 (`session/request_permission`, AGY) | 비대화형 위임 중 에이전트가 권한을 물을 때 | 에이전트가 `stdout`으로 보낸 역방향 JSON-RPC 요청(`id`와 `method` 동시 존재)을 감지해 즉시 승인 응답을 `stdin`으로 돌려준다. |
| 비선점 턴 취소 (`session/cancel`, AGY) | 긴 추론이나 잘못된 방향의 턴을 멈출 때 | 턴 실행 락(`turnMu`)과 표준입력 쓰기 락(`writeMu`)을 분리했다. 실행 도중에도 `session/cancel` 알림을 즉시 주입해 프로세스를 살려둔 채 현재 턴만 중단한다. |
| 런타임 모드·모델 전환 (`acp_session_control`, AGY) | 대화 도중 실행 모드나 모델을 바꿀 때 | 살아있는 세션에 `session/set_mode`, `session/set_config_option`, `session/set_model`, `session/close` RPC를 전송한다. |
| 명명 세션(`-s`) 및 비동기 큐(`-no-wait`) | UUID 대신 이름으로 세션을 관리하거나 백그라운드로 작업을 던질 때 | `-s backend` 같은 이름을 `sessionId`·`threadId`와 매핑해 보드 파일에 영속 저장한다. 위임 도구 셋 모두 지원한다. `-no-wait`(`async: true`)를 주면 백그라운드 고루틴에서 실행하고 즉시 `task_id`를 반환한다. |

---

## 2. 아키텍처와 통신 프로토콜

### 2.1 전체 구조

```
┌────────────────┐ ┌────────────────┐ ┌────────────────┐ ┌────────────────┐
│      AGY       │ │     Codex      │ │  Claude Code   │ │사람 · 스크립트 │
└────────────────┘ └────────────────┘ └────────────────┘ └────────────────┘
     MCP stdio          MCP stdio          MCP stdio       CLI · A2A HTTP
         │                  │                  │                  │
         ▼                  ▼                  ▼                  ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ a2a-acp-bridge (Go 단일 바이너리) · internal/ 패키지                    │
│ cli ─▶ mcp · a2a ─▶ dispatch (도구 이름 → 실행기 라우팅)                │
│                                                                         │
│ ┌───────────────┐ ┌───────────────┐ ┌───────────────┐ ┌───────────────┐ │
│ │ acp           │ │ codex         │ │ claude        │ │ board         │ │
│ │ SessionPool   │ │ exec · resume │ │ -p JSON 러너  │ │ 태스크·메시지 │ │
│ │ 상주 워커·취소│ │ JSONL 파서    │ │ 권한 모드     │ │ 명명 세션     │ │
│ └───────────────┘ └───────────────┘ └───────────────┘ └───────────────┘ │
└─────────────────────────────────────────────────────────────────────────┘
          │                 │                 │                 │
          ▼                 ▼                 ▼                 ▼
       agy acp         codex exec         claude -p      a2a_board.json · coord.json
     (ACP stdio)         (JSONL)           (JSON)       (각 파일 flock·원자 교체)
```

- 위쪽이 호출자다. 에이전트는 MCP로, 사람과 스크립트는 CLI나 A2A HTTP로 부른다. 어느 호출자든 아래쪽 어느 에이전트에게든 위임할 수 있다.
- AGY는 `agy acp --dangerously-skip-permissions`를 상주 자식 프로세스로 띄운다. Codex와 Claude Code는 호출할 때마다 프로세스를 새로 띄운다. 상주 풀과 `acp_session_control`은 AGY에만 있다.
- 위임 도구는 실행 전후로 보드에 메시지와 태스크를 기록한다(2.4절).

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
   작업 디렉터리에 `.claude/state/personal-gate/current` 파일이 있으면 활성 사용자 이름을 읽는다. 첫 턴 응답이 사용자 확인을 요구하면 같은 세션 안에서 확인 프롬프트를 자동으로 보내 본답변을 받아온다. AGY와 Codex 위임에 적용하고 Claude Code 위임에는 적용하지 않는다.

### 2.3 Codex·Claude Code 실행 방식

**Codex**
- 새 실행은 `codex -s <sandbox> exec --skip-git-repo-check --json <prompt>`다. `sandbox`는 `read-only` 또는 `workspace-write`이고 새 실행의 기본값은 `workspace-write`다.
- 스레드를 이어갈 때는 `codex -s <sandbox> exec resume --skip-git-repo-check --json <thread_id> <prompt>`를 쓴다. Codex 0.170.0의 재개는 기존 스레드 권한을 복원하지 않고 현재 설정을 다시 적용하므로, 브리지는 재개 시 `sandbox`를 생략하면 `read-only`를 적용한다. 쓰기 권한이 필요하면 `workspace-write`를 명시한다.
- 호출자와 대상 Codex의 `CODEX_HOME`이 다르면 `A2A_CODEX_HOME`으로 대상 홈을 지정한다. 브리지는 자식 Codex에만 이 값을 적용하고 `CODEX_SESSION_ID`·`CODEX_THREAD_ID`·`CODEX_TOOL_CALL_ID`는 상속하지 않는다.
- stdout의 JSONL에서 `thread.started`의 `thread_id`와 `item.completed` 중 `agent_message`의 텍스트를 모은다. 마지막 메시지가 `response`이고 전부가 `all_messages`다. JSONL 한 줄은 최대 4 MiB까지 읽는다.
- stderr는 본문 파싱에 섞지 않고 오류 진단에만 덧붙인다. 실행 실패, 타임아웃, 취소는 stderr와 함께 오류로 전달되고 태스크는 `failed`가 된다.

**Claude Code**
- 실행은 `claude -p --output-format json --permission-mode <mode>`다. 이어갈 때는 `--resume <session_id>`를 붙인다. 프롬프트는 stdin으로 넘겨서 `-`로 시작하는 글도 플래그로 오인되지 않는다.
- `permission_mode`는 `plan`, `default`, `acceptEdits` 중 하나다. 기본값 `plan`은 읽기 전용이다. 파일을 고치게 하려면 `acceptEdits`를 명시한다.
- 결과 JSON의 `result`, `session_id`, `stop_reason`, `total_cost_usd`를 `response`, `session_id`, `stop_reason`, `cost_usd`로 돌려준다. `is_error`가 true이면 오류로 처리한다.
- 호출마다 비용이 든다. 응답의 `cost_usd`로 확인한다.

### 2.4 공유 보드와 발신자 기록

보드는 태스크, 메시지, 명명 세션을 `a2a_board.json`에 저장한다. 지휘권 리스는 `coord.json`에 따로 저장한다(8절). 두 파일은 서로 다른 flock으로 보호한다.

**위임 한 번의 기록**
1. 위임 도구를 부르면 호출자에서 대상 에이전트로 가는 `delegate` 메시지와 `task_id`가 생긴다.
2. 동기 호출은 실행이 끝나면 대상 에이전트의 응답을 `reply` 메시지로 호출자 수신함에 남기고 태스크를 `completed`로 바꾼다. 실행이 실패하면 태스크를 `failed`로 바꾼다.
3. `async: true`(CLI `-no-wait`)는 태스크를 `working`으로 두고 즉시 `task_id`를 돌려준다. 끝나면 같은 방식으로 `reply`와 `completed`가 기록된다. `a2a_task_status`로 확인한다.

**발신자 결정 순서**
1. 도구 인자 `from_agent`(CLI `-from`)
2. 환경변수 `A2A_AGENT_ID`
3. MCP `initialize`의 `clientInfo.name` 추정(MCP 서버만). 이름에 `codex`가 있으면 `codex`, `antigravity`·`agy`·`gemini`가 있으면 `agy`, `claude`·`claudedesktop`이 있으면 `claude`다.
4. 모르면 `user`

`reply`는 이 발신자 앞으로 쌓인다. `a2a_receive_messages`(CLI `inbox <id>`)로 읽는다. `agent_id`는 `agy`, `codex`, `claude`, `user` 중 하나다. 기본 동작은 읽지 않은 메시지 최대 20건을 돌려주고 읽음 처리한다.

`agents.json`을 읽거나 파싱하지 못하면 내장 에이전트로 계속 동작한다. `a2a_discover_agents`와 `/.well-known/agent.json` 응답의 `registry_warnings`에 파일 경로와 오류를 남긴다.

명명 세션은 에이전트별로 따로 저장된다. 같은 이름 `backend`를 AGY와 Claude Code에 각각 써도 별개 레코드다.

---

## 3. 소스 구조

`cmd/` 에는 진입점만 두고, 기능은 `internal/` 패키지로 나눈다. 패키지 하나가 책임 하나를 맡고, 파일은 300줄 이하로 유지한다.

```
cmd/a2a-acp-bridge/   main — cli.Run 호출만
internal/
  cli/                서브커맨드·플래그(-timeout, -cwd, -from) 해석
  dispatch/           도구 스키마(Tools)와 도구 이름 → acp·codex·claude·board 라우팅, 발신자 결정, 비동기 큐
  acp/                ACP 프로토콜 파서, 상주 워커, 세션 풀, session/list
  acp/acptest/        테스트용 모의 ACP 에이전트(테스트 바이너리를 자식 프로세스로 재실행)
  codex/              codex exec/resume JSONL 러너
  claude/             claude -p JSON 러너
  board/              공유 보드(태스크·메시지·명명 세션)와 coord.json 리스, 파일별 flock + 원자 저장
  agents/             A2A Agent Card(agy·codex·claude), 발신자 ID 목록·MCP 클라이언트 이름 추정
  mcp/                MCP stdio JSON-RPC 서버
  a2a/                A2A HTTP 서버(/.well-known/agent.json, /a2a)
  jsonrpc/            MCP·A2A HTTP 가 함께 쓰는 JSON-RPC 요청 타입
  gate/               개인 게이트(.claude/state/personal-gate) 감지
```

의존 방향은 `cli → mcp·a2a → dispatch → acp·codex·claude·board·agents` 한 방향이다. 하위 패키지는 상위 패키지를 import 하지 않는다. 테스트는 패키지마다 따로 있고, 모의 ACP 서버가 필요한 패키지는 `TestMain` 에서 `acptest.Main(m)` 을 부른다.

---

## 4. 설치 및 MCP 등록

### 4.1 빌드 및 테스트

```sh
go vet ./...
go test -race -v ./...
go build -o ~/.local/bin/a2a-acp-bridge ./cmd/a2a-acp-bridge
```

### 4.2 로컬 MCP 등록

```sh
agy mcp add a2a-acp-bridge -- ~/.local/bin/a2a-acp-bridge mcp
codex mcp add a2a-acp-bridge -- ~/.local/bin/a2a-acp-bridge mcp
claude mcp add a2a-acp-bridge -- ~/.local/bin/a2a-acp-bridge mcp
```

`claude mcp add`의 기본 범위는 현재 프로젝트(`local`)다. 모든 프로젝트에서 쓰려면 `-s user`를 붙인다.

MCP로 붙은 에이전트의 이름을 인식하지 못하면 발신자가 `user`로 기록된다. 발신자를 확실히 고정하려면 등록할 때 `A2A_AGENT_ID`를 환경변수로 넘긴다.

```sh
agy mcp add --env A2A_AGENT_ID=agy a2a-acp-bridge -- ~/.local/bin/a2a-acp-bridge mcp
codex mcp add --env A2A_AGENT_ID=codex a2a-acp-bridge -- ~/.local/bin/a2a-acp-bridge mcp
claude mcp add a2a-acp-bridge -e A2A_AGENT_ID=claude -- ~/.local/bin/a2a-acp-bridge mcp
```

호출자와 대상 Codex가 서로 다른 홈을 쓰면 대상 홈도 함께 넘긴다. 예를 들어 DeepSeek Codex에서 정식 Codex를 호출할 때는 다음과 같이 등록한다.

```sh
codex mcp add \
  --env A2A_AGENT_ID=codex \
  --env A2A_CODEX_HOME=/Users/me/.codex \
  a2a-acp-bridge -- ~/.local/bin/a2a-acp-bridge mcp
```

### 4.3 SSH 원격 호스트 MCP 등록

원격 서버에 `a2a-acp-bridge`와 `agy`·`codex`·`claude`를 두면 로컬에서 SSH 표준 입출력으로 원격 에이전트 풀을 부를 수 있다.

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
claude mcp add remote-a2a-bridge -- ~/.local/bin/remote-a2a-mcp
```

MCP stdio 스트림이 깨지지 않도록 `ssh -q -o LogLevel=ERROR` 옵션을 준다. 그래야 SSH 배너나 호스트 키 경고가 표준출력에 섞이지 않는다.

---

## 5. MCP 도구 상세 명세

MCP 서버(`a2a-acp-bridge mcp`)는 아래 9개 도구를 노출한다(2026-11-22 기준, main `6c6d655`).

| 도구 이름 | 언제 쓰나 | 필수 인자 | 선택 인자 |
| :--- | :--- | :--- | :--- |
| `a2a_discover_agents` | 사용 가능한 에이전트 카드(내장 `agy`·`codex`·`claude` + `agents.json` 동적 에이전트), 열린 명명 세션, 최근 태스크 10건, 레지스트리 경고를 확인할 때 | 없음 | 없음 |
| `a2a_coordinator_lease` | 지휘권 리스를 조회·선점·갱신·이양할 때 (`action`: `status`/`claim`/`renew`/`handoff`) | `action`; 변경 시 `agent_id` | `expected_term`, `ttl_seconds`, `force`, `reason`, `to_agent`, `active_agents` |
| `acp_delegate_to_agy` | AGY(`agy acp`)에게 코드 작성·분석·리팩터링을 맡길 때 | `prompt` | `session_name`, `session_id`, `mode`, `model`, `async`, `cwd`, `from_agent` |
| `a2a_delegate_to_codex` | Codex(`codex exec`)에게 교차 코드 리뷰나 비대화형 작업을 맡길 때 | `prompt` | `session_name`, `thread_id`, `sandbox`, `async`, `cwd`, `from_agent` |
| `a2a_delegate_to_claude` | Claude Code(`claude -p`)에게 교차 리뷰나 분석을 맡길 때 | `prompt` | `session_name`, `session_id`, `permission_mode`, `async`, `cwd`, `from_agent` |
| `acp_session_control` | 상주 AGY 세션 상태를 보거나 실행 중 취소·모드 변경·모델 변경·종료할 때 | `action` | `session`, `mode`, `model`, `cwd` |
| `a2a_send_message` | 에이전트 직접 실행 없이 공유 보드에 메시지·태스크만 남길 때 (`intent`: `delegate`/`review`/`reply`/`chat`/`sync`) | `to_agent`, `content` | `from_agent`, `intent`, `task_id` |
| `a2a_receive_messages` | 특정 에이전트(내장·동적 에이전트 또는 `user`) 앞으로 온 메시지를 읽을 때. `mark_read: false` 면 읽음 처리하지 않는다 | `agent_id` | `unread_only`, `mark_read` |
| `a2a_task_status` | 특정 `task_id`의 진행 상태와 대화 내역을 조회하거나 상태를 바꿀 때 (`state`: `submitted`/`working`/`input-required`/`completed`/`failed`) | 없음 | `task_id`, `state` |

`from_agent`는 등록된 에이전트 ID(내장 `agy`·`codex`·`claude`와 `agents.json` 의 동적 에이전트) 또는 `user` 다. 생략하면 2.4절의 순서로 정한다. `a2a_send_message`의 `to_agent`는 등록된 에이전트 ID 중 하나다. 동적 에이전트는 `A2A_AGENTS_FILE`(기본 `~/.local/share/a2a-acp-bridge/agents.json`)에 소문자 slug(`^[a-z0-9][a-z0-9-]{0,23}$`)로 추가한다. 파일이 깨져 있으면 경고 없이 동적 에이전트가 빠지니 `cards` 로 확인한다(#20). `a2a_task_status`는 `task_id`를 생략하면 최근 태스크 20건을 돌려준다.

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

### 5.2 `a2a_delegate_to_claude` 호출 및 응답 예

요청 인자:
```json
{
  "prompt": "internal/board의 락 처리를 읽기 전용으로 리뷰해줘",
  "session_name": "review",
  "permission_mode": "plan",
  "from_agent": "codex"
}
```

응답:
```json
{
  "agent": "claude",
  "protocol": "Claude-Code-print-json",
  "session_id": "11111111-2222-3333-4444-555555555555",
  "session_name": "review",
  "task_id": "task-a94130a6f8",
  "permission_mode": "plan",
  "response": "flock과 임시 파일 교체 순서에 문제가 없습니다.",
  "stop_reason": "end_turn",
  "cost_usd": 0.34
}
```

`async: true`이면 `async: true`, `task_id`와 빈 `response`를 즉시 돌려준다. 결과는 `a2a_task_status`나 호출자 수신함(`reply`)에서 읽는다. Codex 응답(`a2a_delegate_to_codex`)은 `session_id` 대신 `thread_id`를 주고, 모든 `agent_message`를 `all_messages`로 함께 준다.

### 5.3 `a2a_coordinator_lease` 지휘권 리스

여러 에이전트 중 한 명만 지휘자(오케스트레이터)를 맡도록 `coord.json`에 `term`·`lease_until` 리스를 둔다. 모든 변경은 `coord.json.lock` flock 아래에서 `expected_term`을 비교(CAS)한 뒤에만 반영된다. 새 보드 저장은 `a2a_board.json`에 리스를 기록하지 않는다.

- `status`: 현재 리스와 `vacant`, `active`, `expired`, `handed_off` 가운데 하나인 `status`를 돌려준다. 리스가 없으면 `coordinator: null`, `status: "vacant"`다.
- `claim`: `expected_term` 이 현재 term 과 같을 때만 `term + 1` 로 선점한다. 활성 리스를 만료 전에 가져오려면 `force` 와 `reason` 이 함께 필요하다.
- `renew`: 현재 지휘자만 `ttl_seconds`(CLI `-ttl`, 기본 60분)만큼 연장한다.
- `handoff`: 현재 지휘자가 `to_agent` 에게 넘긴다. `to_agent` 를 비우면 누구나 claim 할 수 있는 개방 이양이 된다.
- 변경에는 `agent_id`가 필요하다. `agent_id`가 없거나 `user`면 claim·renew·handoff를 거부한다. 식별되지 않은 여러 프로세스가 같은 `user`로 리스를 넘겨받는 문제를 막는다.
- 실패 사유는 term 불일치, 활성 리스, 만료된 리스 갱신·이양이다. 개방 이양 재이양, 지휘자 아님, 사유 없는 force, 잘못된 ID, 익명 변경자도 거부한다.

주의: #16 이전 바이너리가 `a2a_board.json`을 덮어써도 `coord.json` 리스는 유지된다(#20). 그 프로세스는 새 리스 저장소를 읽지 못한다. 바이너리를 바꾼 뒤 모든 브리지 프로세스를 다시 띄운다.

### 5.4 `acp_session_control` 동작(`action`) 목록

AGY의 상주 ACP 세션에만 적용한다. Codex와 Claude Code는 호출마다 프로세스를 새로 띄우므로 해당하지 않는다.

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

### 명령 예

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

# 4-1. 다른 CODEX_HOME의 Codex 스레드에 위임 (재개 기본 샌드박스: read-only)
THREAD_ID=01234567-89ab-cdef-0123-456789abcdef
A2A_CODEX_HOME=/Users/me/.codex A2A_AGENT_ID=codex a2a-acp-bridge ask-codex -thread-id "$THREAD_ID" -cwd /path/to/repo -timeout 5m "현재 작업 상태를 알려줘"

# 5. Claude Code에게 읽기 전용 리뷰 위임 (기본 plan), 같은 -s 이름으로 이어가기
a2a-acp-bridge ask-claude -s review -cwd /path/to/repo -timeout 5m "internal/board 락 순서를 검토해줘"
a2a-acp-bridge ask-claude -s review "방금 지적한 부분을 어떻게 고치면 되는지 알려줘"

# 6. Claude Code에게 파일 수정까지 허용
a2a-acp-bridge ask-claude -permission-mode acceptEdits -cwd /path/to/repo "README 오타를 고쳐줘"

# 7. 세션 상태 조회 및 런타임 제어 (AGY 상주 세션 대상, sessions 는 status 로도 부른다)
a2a-acp-bridge sessions
a2a-acp-bridge set-mode -s backend -mode plan
a2a-acp-bridge set-model -s backend -model gemini-2.5-pro
a2a-acp-bridge cancel -s backend
a2a-acp-bridge close -s backend

# 8. 공유 보드 메시지 송신(delegate|review|reply|chat|sync) 및 수신함 확인
a2a-acp-bridge send -from agy -to codex -intent sync "머지 및 로컬 서버 기동 동기화"
a2a-acp-bridge inbox codex
a2a-acp-bridge inbox -all claude

# 9. 발신자를 밝혀서 위임하면 응답(reply)이 그 발신자의 수신함으로 돌아온다
A2A_AGENT_ID=codex a2a-acp-bridge ask-claude "이 변경의 위험을 짚어줘"
a2a-acp-bridge inbox codex
a2a-acp-bridge ask-agy -from claude "테스트를 추가해줘"
a2a-acp-bridge inbox claude
# 발신자를 밝히지 않으면 user 수신함으로 돌아온다
a2a-acp-bridge inbox user

# 10. 공유 보드 폴링 감시 (새 메시지·읽음·태스크 상태·지휘권 리스 변화를 JSON 이벤트로 출력)
a2a-acp-bridge watch
a2a-acp-bridge watch -agent codex -interval 2s -timeout 5m
a2a-acp-bridge watch -all

# 11. A2A HTTP 서버 기동
a2a-acp-bridge serve-a2a -addr 127.0.0.1:8765
```

### 서브커맨드별 플래그

플래그는 프롬프트(위치 인자) 앞에 둔다. `-timeout` 기본값은 3분이다.

| 서브커맨드 | 플래그 |
| :--- | :--- |
| `ask-agy` | `-s`, `-session-id`, `-mode`(`auto`·`accept-edits`·`plan`), `-model`, `-no-wait`, `-cwd`, `-timeout`, `-from` |
| `ask-codex` | `-s`, `-thread-id`, `-sandbox`(`read-only`·`workspace-write`, 새 실행 기본 `workspace-write`, 재개 기본 `read-only`, `-s` 명명 세션은 저장된 sandbox 재사용), `-no-wait`, `-cwd`, `-timeout`, `-from` |
| `ask-claude` | `-s`, `-session-id`, `-permission-mode`(`plan`·`default`·`acceptEdits`, 기본 `plan`), `-no-wait`, `-cwd`, `-timeout`, `-from` |
| `agy-sessions` | `-cursor`, `-cwd`, `-timeout` |
| `sessions`·`cancel`·`set-mode`·`set-model`·`close` | `-s`(또는 첫 위치 인자), `-mode`, `-model`, `-cwd`, `-timeout` |
| `send` | `-from`, `-to`(기본 `codex`), `-intent`(기본 `delegate`), `-task-id` |
| `inbox` | `-all`(읽은 메시지 포함), `-peek`(읽음 처리하지 않음), 위치 인자로 에이전트 ID 또는 `user` |
| `coord` | 위치 인자 `status`·`claim`·`renew`·`handoff`, `-agent`(기본 `A2A_AGENT_ID`, 변경 시 필수), `-expected-term`(CAS 용 현재 term), `-ttl`(기본 60분), `-force`(만료 전 강제 승계, `-reason` 필요), `-reason`, `-to`(handoff 대상, 비우면 개방 이양), `-agents`(활성 에이전트 목록, 쉼표 구분) |
| `watch` | `-agent`(기본 `A2A_AGENT_ID` 또는 `user`), `-interval`(기본 `1s`), `-all`, `-timeout`(기본 `0`, 종료 시그널까지) |
| `serve-a2a` | `-addr`(기본 `127.0.0.1:8765`) |

`-from`의 값은 등록된 에이전트 ID(내장 `agy`·`codex`·`claude`와 `agents.json` 의 동적 에이전트) 또는 `user` 다. 없으면 `A2A_AGENT_ID`, 그것도 없으면 `user`로 기록한다. `-no-wait`는 결과 JSON을 즉시 출력하지만, CLI 프로세스는 백그라운드 작업이 끝날 때까지 종료하지 않는다. 백그라운드 작업에도 `-timeout` 이 적용된다(MCP 서버의 async 작업은 상한 없음). `-cwd` 를 주지 않고 `-s` 로 명명 세션을 이어가면 그 세션을 만든 디렉터리에서 실행한다. Claude Code·ACP 세션은 프로젝트 디렉터리에 묶여 있어 다른 디렉터리에서는 이어지지 않기 때문이다. `inbox`는 조회한 메시지를 읽음 처리한다(`-all`을 줘도 같다). 남의 수신함을 확인만 할 때는 `-peek` 을 쓴다. `watch`는 보드를 폴링해서 관련 에이전트(또는 `-all`)의 새 메시지(`type: message`, `op: new`), 읽음(`op: read`), 태스크 상태 변화(`type: task`), 지휘권 리스 변화(`type: coordinator`, `op: state`)를 JSON 이벤트 한 줄씩 출력하고, 저장소를 바꾸지 않는다. 리스 이벤트는 `from_agent`(직전 리더)·`to_agent`(현 리더)·`content`(사유)와 리스 전체(`coordinator`)를 담고, 리더·직전 리더·이양 대상·`active_agents` 에 든 에이전트(또는 `-all`)에게 나온다. `coord renew` 로 만료 시각만 바뀌어도 나온다. 한 주기 안에 생성과 읽음이 함께 일어나면 `op: new` 와 `op: read` 가 둘 다 나온다. 다만 폴링이라 한 주기 안의 태스크 중간 상태와 리스 중간 변화는 건너뛰고 마지막 상태만 보인다(#21).

---

## 7. A2A HTTP 서버 API

`a2a-acp-bridge serve-a2a -addr 127.0.0.1:8765`로 기동하면 아래 HTTP 엔드포인트가 열린다. 느린 헤더 공격을 막기 위해 `ReadHeaderTimeout: 5s`와 요청 본문 `1 MiB` 상한이 기본 적용되어 있다. 인증이 없고 위임 도구가 로컬 에이전트를 실행하므로 기본 주소(`127.0.0.1`)를 바꾸지 않는다.

| HTTP 메서드 및 경로 | 설명 |
| :--- | :--- |
| `GET /.well-known/agent.json` | A2A 표준 Agent Card(`agy`·`codex`·`claude`), 명명 세션, 최근 태스크 JSON 반환 |
| `GET /agents` | `/.well-known/agent.json`과 동일 |
| `POST /a2a` | JSON-RPC 2.0 요청 본문을 받아 도구 실행 후 결과 반환 |

`POST /a2a`에서 지원하는 JSON-RPC `method` 매핑:

| JSON-RPC `method` | 연결되는 내부 도구 |
| :--- | :--- |
| `acp/agy` | `acp_delegate_to_agy` |
| `acp/control` | `acp_session_control` |
| `a2a/codex` | `a2a_delegate_to_codex` |
| `a2a/claude` | `a2a_delegate_to_claude` |
| `tasks/send` | `a2a_send_message` |
| `tasks/get` | `a2a_task_status` |

호출 예:
```sh
curl -s http://127.0.0.1:8765/a2a \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"acp/control","params":{"action":"status"}}'

curl -s http://127.0.0.1:8765/a2a \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"a2a/claude","params":{"prompt":"internal/board 락 처리를 리뷰해줘","from_agent":"codex"}}'
```

`params`는 MCP 도구 인자와 같다. `from_agent`를 생략하면 서버를 띄운 프로세스의 `A2A_AGENT_ID`, 그것도 없으면 `user`로 기록한다.

---

## 8. 환경변수와 저장소 파일

| 환경변수 | 기본값 | 설명 |
| :--- | :--- | :--- |
| `A2A_BRIDGE_STORE` | `~/.local/share/a2a-acp-bridge/a2a_board.json` | 태스크·메시지·명명 세션을 저장하는 JSON 파일 경로. 지휘권 리스는 같은 디렉터리의 `coord.json`에 저장한다 |
| `A2A_ACP_COMMAND` | `agy acp --dangerously-skip-permissions` | ACP 워커가 실행할 명령어와 인자 재정의 |
| `A2A_CODEX_COMMAND` | `codex` | Codex 위임 시 실행할 바이너리 명령 재정의. 공백으로 나눈 첫 토큰이 실행 파일이고 나머지는 인자 앞에 붙는다 |
| `A2A_CODEX_HOME` | (없음) | Codex 자식 프로세스에만 적용할 `CODEX_HOME`. DeepSeek Codex에서 정식 Codex처럼 다른 홈의 Codex를 호출할 때 지정한다 |
| `A2A_CLAUDE_COMMAND` | `claude` | Claude Code 위임 시 실행할 바이너리 명령 재정의. 규칙은 `A2A_CODEX_COMMAND`와 같다 |
| `A2A_AGENTS_FILE` | `~/.local/share/a2a-acp-bridge/agents.json` | 내장 3종 외에 참여할 동적 에이전트 정의 파일. 같은 ID 면 내장 카드를 덮어쓴다 |
| `A2A_AGENT_ID` | (없음) | 이 프로세스를 부르는 에이전트 ID(내장·동적 에이전트 slug). 위임·`send` 에 `from_agent`·`-from` 이 없으면 이 값을 발신자로 기록하고, 이것도 없으면 `user` 로 기록한다. MCP 서버는 이 값이 비어 있을 때만 initialize 의 `clientInfo.name` 으로 발신자를 추정한다 |

보드 저장소(`a2a_board.json`)와 지휘권 리스(`coord.json`)는 각각 내부 뮤텍스와 OS 파일 락으로 동시 쓰기를 직렬화한다. 락 파일은 `a2a_board.json.lock`과 `coord.json.lock`이다. `flock`은 Linux, macOS, BSD 계열에서 동작한다. 그 밖의 OS에서는 내부 뮤텍스만 쓴다.

두 저장소 모두 같은 디렉터리에 `os.CreateTemp`로 임시 파일을 만든다. 권한 `0600`으로 쓰고 `os.Rename`으로 원자 교체한다. 에이전트 셋이 각자 `mcp` 프로세스를 띄워 같은 보드를 써도 안전하다. 단 PR #6 이전 빌드는 임시 파일 경로가 고정이라 동시에 쓰면 rename이 실패한다.

`coord.json`이 없고 기존 `a2a_board.json`에 `coordinator`가 있으면 첫 coord 접근 때 한 번 이전한다. 두 파일의 락을 동시에 잡지 않으므로 기존 보드 쓰기와 교착되지 않는다. 이후 새 보드 저장은 `coordinator` 필드를 쓰지 않는다.

---

## 9. 협업 규칙

이 레포는 Claude Code, AGY, Codex가 함께 작업한다. 규칙 원본은 [AGENTS.md](AGENTS.md)이고, 기준 이슈는 고정 이슈 #8이다. 여기에 규칙을 복제하지 않는다. 요약만 적는다.

- 이슈의 `owner:*` 라벨이 담당이다. 라벨이 붙은 이슈는 남의 작업이므로 손대지 않는다.
- 담당이 멈추면 `handoff` 라벨이나 이어받기 예고 절차로 다른 에이전트가 이어받는다.
- 레포는 공개다. `main`은 브랜치 보호로 PR이 필수이고(관리자 포함), force push와 삭제는 막혀 있다. 머지는 merge commit만 허용한다(squash·rebase 비활성).
