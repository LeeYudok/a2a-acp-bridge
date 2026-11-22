# AGENTS.md — a2a-acp-bridge

이 레포에서 일하는 AI 에이전트(Claude Code·AGY·Codex)가 따르는 규칙이다. 사람용 개요와 사용법은 [README.md](README.md) 를 본다.

## 구조

`cmd/a2a-acp-bridge` 는 진입점만 두고, 기능은 `internal/` 패키지로 나눈다. 의존 방향은 `cli → mcp·a2a → dispatch → acp·codex·claude·board·agents` 한 방향이다. 하위 패키지는 상위 패키지를 import 하지 않는다.

| 패키지 | 책임 |
|---|---|
| `internal/cli` | 서브커맨드·플래그, `watch`(보드 폴링)·`coord`(지휘권 리스) |
| `internal/dispatch` | 도구 스키마(`Tools`)와 도구 이름 → acp·codex·claude·board 라우팅, 발신자(`Caller`·`from_agent`), 지휘권 리스 도구 |
| `internal/acp` | ACP 프로토콜, 상주 워커, 세션 풀, `session/list` |
| `internal/acp/acptest` | 테스트용 모의 ACP 에이전트 |
| `internal/codex` | `codex exec` 러너 |
| `internal/claude` | `claude -p --output-format json` 러너(Claude Code 위임) |
| `internal/board` | 공유 보드(태스크·메시지·명명 세션)와 `coord.json` 지휘권 리스, 파일별 flock + 임시 파일 원자 저장 |
| `internal/agents` | Agent Card, 내장·동적(`agents.json`) 에이전트 레지스트리, MCP clientInfo 추정 |
| `internal/monitor` | 공유 보드와 지휘권 리스에서 본문·제목·`cwd`·세션 ID를 뺀 읽기 전용 모니터링 스냅샷을 만든다 |
| `internal/mcp`, `internal/a2a` | MCP stdio 서버, A2A HTTP 서버 |
| `internal/jsonrpc`, `internal/gate` | 공용 JSON-RPC 요청 타입, 개인 게이트 감지 |

새 기능은 책임이 맞는 패키지에 넣는다. 맞는 패키지가 없으면 새 패키지를 만든다. 루트에 `.go` 파일을 두지 않는다.

## 명령

```sh
gofmt -l .                  # 출력이 없어야 한다
go vet ./...
go test -race ./...
go build -o ~/.local/bin/a2a-acp-bridge ./cmd/a2a-acp-bridge
```

- Go 1.22 표준 라이브러리만 쓴다. `go.mod` 에 의존성을 추가하지 않는다.
- 파일은 300줄 이하, 함수 인지 복잡도는 15 이하로 유지한다.
- 모의 ACP 서버가 필요한 테스트 패키지는 `func TestMain(m *testing.M) { acptest.Main(m) }` 을 두고 `acptest.Command(t)` 를 세션 풀 명령으로 쓴다.

## 이슈·브랜치·PR

- 작업마다 GitHub 이슈를 먼저 연다. 브랜치는 `batch/<agent>-<YYYYMMDD>-<8자리 hex>`(`openssl rand -hex 4`) 이다.
- 커밋 제목은 `[<모델명>] <type>: <설명> (#N)` 이다. 커밋 작성자는 에이전트 계정(`claude-ai`·`codex-ai`·`agy-ai` `@doksam.com`)으로 한다.
- PR 본문에 `Closes #N` 을 쓴다. 머지는 merge commit 으로 한다(squash 금지).
- **자동 머지 규칙(2026-11-22 사용자 승인)**: 타 에이전트 교차 리뷰(LGTM 또는 지적 사항 해소 확인)와 필수 CI 체크가 모두 통과한 PR은 현재 지휘자(오케스트레이터)가 merge commit(`--merge`, squash 금지)으로 즉시 머지하고 결과를 보고한다. 교차 리뷰 미완료·CI 실패·정책/보안 쟁점이 남은 PR만 사용자 확인을 기다린다.
- `main` 에 직접 push 하지 않는다. 항상 `batch/*` 브랜치와 PR 로 올린다. `main` 은 GitHub 브랜치 보호(PR 필수, 관리자 포함, force push·삭제 금지)가 걸려 있고, 머지는 merge commit 만 허용한다(2026-11-22 public 전환 후 적용).
- 혼자 하는 작업이 아니면 에이전트마다 자기 git worktree 를 쓴다. 남의 워크트리를 직접 고치지 않는다.

## 협업 규칙 — 이슈 기반 담당·핸드오프·이어받기

사용자 지시(2026-11-22): "다른 에이전트가 응답이 없거나 죽거나 토큰을 다 쓰면 나머지 에이전트가 이어서 처리하게 해, 마무리는 해야지 뭐든", "이슈에 핸드오프를 하던지 해서 서로 겹치지 않게 해". 기준 이슈는 고정 이슈 #8 이다.

### 1. 착수 전 담당 확인

1. 이슈에 `owner:*` 라벨이 있으면 남의 작업이다. 손대지 않는다.
2. 라벨이 없으면 `owner:<나>` 를 붙이고 담당 코멘트를 남긴 뒤 착수한다.

```
담당: <claude|agy|codex>
브랜치: batch/<agent>-<YYYYMMDD>-<hex>
워크트리: <절대 경로>
상태: 착수 | 진행 중(<한 줄>) | PR #N | 머지됨
```

상태가 바뀌면 같은 형식으로 코멘트를 추가한다. 상태는 기억이 아니라 실제 `git status`·브랜치·PR 을 보고 쓴다. 보드(`a2a-acp-bridge send`)는 알림용이고, 기록의 기준은 이슈다.

### 2. 핸드오프 — 내가 멈출 때

컨텍스트·토큰이 바닥나거나 세션을 끝내야 하면, 멈추기 전에 미커밋 변경을 커밋·푸시하고 이슈에 남긴다.

```
핸드오프: <나> → 누구든
브랜치/커밋: <브랜치> @ <SHA>
끝난 것: ...
남은 것: ...
검증 명령: go vet ./... && go test -race ./...
주의: ...
```

그리고 `owner:<나>` 를 떼고 `handoff` 라벨을 붙인다.

### 3. 이어받기 — 남이 멈췄을 때

1. `ask-*` 가 실행기 오류·타임아웃·할당량 초과로 끝나거나, 보드 메시지에 답이 없거나, 담당 이슈가 멈춰 있으면 한 번 다시 묻는다. `handoff` 라벨이 붙은 이슈는 묻지 않고 이어받는다.
2. 다시 물을 때 이슈에 `이어받기 예고: <나>, <YYYY-MM-DD HH:MM> 이후` 를 남긴다. 그 시각이 지나면 이어받기 직전에 `owner:*` 라벨, 원담당 브랜치의 최신 커밋, PR 상태를 다시 확인한다. 그 사이 진척이 있으면 이어받지 않는다.
3. 이어받기 선언이 겹치면 사용자가 한 명을 정한다. 사용자가 없으면 예고 코멘트를 먼저 단 쪽이 맡는다.
4. 원담당의 브랜치·워크트리·미커밋 변경·열린 PR 위에서 이어간다. 새 브랜치나 새 PR 을 따로 만들지 않는다. 남의 워크트리는 덮어쓰지 않고, 브랜치를 받아 자기 워크트리에서 작업한다.
5. 라벨을 `owner:<나>` 로 바꾸고 `이어받음: <원담당> → <나>, 브랜치 …` 를 남긴다. 보드로 원담당에게도 알린다.
6. 원담당이 돌아오면 이슈부터 읽는다. 넘어간 일은 되찾지 않고 리뷰로 돕는다.

### 4. 브리지로 서로 부를 때

- 발신자를 밝힌다: `A2A_AGENT_ID=<나>` 를 두거나 `-from <나>` 를 준다. 없으면 보드에 `user` 로 기록된다.
- 받는 쪽 수신함: `a2a-acp-bridge inbox [-peek] <agy|codex|claude|user|<agent_id>>` (`-peek` 은 읽음 표시 없이 조회한다).
- 서로 다른 `CODEX_HOME` 을 쓰는 Codex끼리는 `A2A_CODEX_HOME` 으로 대상 Codex 홈을 고정한다. 이 값은 브리지가 실행하는 `codex exec` 자식에만 적용된다.
- 실시간 변화 감시: `a2a-acp-bridge watch`(기본 1초 폴링, `-interval`·`-agent`·`-all`·`-timeout`). 새 메시지·읽음·태스크 상태·지휘권 리스 변화를 JSON 이벤트 한 줄로 출력한다. 폴링이라 한 주기 안의 중간 상태는 건너뛴다.
- 서로에게 일을 맡길 때: `ask-agy`·`ask-codex`·`ask-claude`(Claude 는 기본 `-permission-mode plan`, 수정까지 맡길 땐 `acceptEdits`). 응답은 발신자 수신함에도 남는다.
- 브리지 프로세스를 여러 개 동시에 띄울 때는 PR #6 이후 바이너리를 쓴다. 그 이전 바이너리는 보드 `.tmp` 경로가 고정이라 동시에 쓰면 rename 이 실패한다.
- 바이너리를 바꾸면 각 에이전트의 MCP 서버(`a2a-acp-bridge mcp`)까지 모든 브리지 프로세스를 다시 띄운다. PR #16 이전 프로세스가 `a2a_board.json`을 덮어써도 `coord.json` 리스는 지워지지 않는다. 다만 그 프로세스는 새 리스 저장소를 읽지 못하므로 재시작이 필요하다(#20).
- 할당량·토큰 한도에 걸리면 이슈 #8 이나 담당 이슈에 "언제까지 불가" 를 남긴다. 남은 에이전트는 그 사이 그 에이전트 담당 작업을 위 3절대로 처리한다.

### 5. 오케스트레이터 지휘권 승계(Failover) 및 다중 에이전트(N-agent) 확장

- **N-에이전트 등록**: `agy`·`codex`·`claude` 외 에이전트를 `~/.local/share/a2a-acp-bridge/agents.json`(`A2A_AGENTS_FILE`)에 추가할 수 있다. ID는 소문자 slug(`^[a-z0-9][a-z0-9-]{0,23}$`)를 쓴다. 파일 파싱에 실패하면 내장 에이전트는 유지되고 `a2a_discover_agents.registry_warnings`에 오류가 나온다.
- **단일 지휘 리스(Lease)**: 단 한 명만 `term` + `lease_until` 리스를 보유한다. 리스와 CAS는 같은 디렉터리의 `coord.json`과 그 전용 flock으로 처리한다. `a2a-acp-bridge coord <status|claim|renew|handoff>` 또는 MCP 도구 `a2a_coordinator_lease`를 쓴다. claim·renew·handoff에는 식별된 `agent_id`가 필요하다.
- **승계·복귀**: 현 지휘자가 중단·오류·할당량 초과·리스 만료 상태면 남은 에이전트가 이어받는다. 이슈 #8 예고 후 `term + 1` 리스를 확정한다. 명령은 `a2a-acp-bridge coord claim -expected-term <N> [-force]`다. 복귀한 전임 지휘자는 `#8` 및 `coord.json`의 최신 `term`을 인정하고 일반 작업자/리뷰어로 합류한다.

### 6. 계열별 위임 풀

- `pool.json`(기본 `~/.local/share/a2a-acp-bridge/pool.json`, 재정의 `A2A_POOL_FILE`) 설정 예시는 `{"max_total":8,"default":{"min":0,"max":2},"families":{"codex-glm":{"min":1,"max":3}}}`이다. 파일이 없으면 상한은 무제한이고 하한은 0이다.
- `agents.json` 카드의 `family`를 계열로 쓰고, 생략하면 ID를 쓴다. 별도 Codex 라우트는 브리지 프로세스의 `A2A_CODEX_AGENT_ID`로 대상 카드를 정한다.
- 세 위임 도구는 보드 flock 안에서 `working` 태스크를 세고 새 태스크를 기록한다. 같은 풀을 쓰는 프로세스에 같은 보드·풀 설정·에이전트 등록 경로를 지정한다. 하한은 표시와 경고에만 쓰며 에이전트 시작은 운영자가 맡는다.
- `pool status`나 `a2a_pool`의 `status`로 상태를 본다. 설정 변경 권한과 명령 예시는 [README의 계열별 위임 풀](README.md#55-계열별-위임-풀)을 따른다.
