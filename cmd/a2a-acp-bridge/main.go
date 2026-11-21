// a2a-acp-bridge 는 agy(ACP)·codex(JSONL) 에이전트를 MCP·A2A HTTP·CLI 로 잇는 단일 바이너리다.
package main

import (
	"fmt"
	"os"

	"github.com/LeeYudok/a2a-acp-bridge/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "오류: %v\n", err)
		os.Exit(1)
	}
}
