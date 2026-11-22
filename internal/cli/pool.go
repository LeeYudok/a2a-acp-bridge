package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/LeeYudok/a2a-acp-bridge/internal/dispatch"
)

func runPoolCmd(ctx context.Context, d *dispatch.Dispatcher, rest []string) error {
	if len(rest) == 1 && rest[0] == "status" {
		res, err := d.CallTool(ctx, "a2a_pool", map[string]any{"action": "status"})
		return printResult(res, err)
	}
	if len(rest) < 2 || rest[0] != "set" {
		return fmt.Errorf("사용법: pool status | pool set <family> --min N --max M | pool set --max-total N")
	}
	args, err := poolSetArgs(rest[1:])
	if err != nil {
		return err
	}
	actor := d.PoolActor
	if actor == "" {
		actor = "user"
	}
	res, err := d.CallPool(args, actor)
	return printResult(res, err)
}

func poolSetArgs(rest []string) (map[string]any, error) {
	args := map[string]any{"action": "set"}
	if !strings.HasPrefix(rest[0], "-") {
		args["family"] = rest[0]
		rest = rest[1:]
	}
	fs := flag.NewFlagSet("pool set", flag.ContinueOnError)
	minimum := fs.Int("min", 0, "계열 하한")
	maximum := fs.Int("max", 0, "계열 상한")
	total := fs.Int("max-total", 0, "전체 상한")
	if err := fs.Parse(rest); err != nil {
		return nil, err
	}
	if fs.NArg() != 0 {
		return nil, fmt.Errorf("pool set: 예상하지 못한 인자 %q", fs.Arg(0))
	}
	values := map[string]int{"min": *minimum, "max": *maximum, "max-total": *total}
	fs.Visit(func(f *flag.Flag) {
		args[strings.ReplaceAll(f.Name, "-", "_")] = values[f.Name]
	})
	return args, nil
}
