package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/codex"
	"codex-netcap/internal/config"
	"codex-netcap/internal/console"
	"codex-netcap/internal/control"
	"codex-netcap/internal/guard"
	"codex-netcap/internal/runtime"
)

const usage = `用法：netcap [--config netcap.json] COMMAND

  serve              使用独立 console 配置启动本地 Web（M2 Alpha）
  doctor --console   只读检查 console 的实例、版本、配置与路由，不调用模型
  run                自动开始：临时日期探测 → 原生 Goal 清单 → 持久天气轮询
  doctor             只读检查 CLI proxy、版本和已加载会话
  status | sessions | goals | events
  pause              暂停自动运行，暂停自有 Goal，停止自有天气/探测
  resume             人工介入后显式恢复，先重新探测
  probe              登记一次临时日期探测
  goal-retry ID      重试当前阻塞 Goal，不绕过原生预算/用量限制
  goal-order ID...   调整清单顺序，不切换当前 Goal

旧 run 的工作会话使用无审批、无沙箱权限；serve 使用显式选择的权限策略。
Goal 文件、工作目录和天气参数在 JSON 配置中设置。不再使用 watch/task-add。
`

func main() {
	if handled, err := runtime.RunClaudeSettingsFileExec(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			os.Exit(1)
		}
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("netcap", flag.ContinueOnError)
	fs.SetOutput(errOut)
	path := fs.String("config", "", "JSON 配置文件")
	fs.Usage = func() { fmt.Fprint(errOut, usage) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	args = fs.Args()
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return nil
	}
	if args[0] == "serve" {
		if len(args) != 1 {
			return fmt.Errorf("serve 不接受额外参数")
		}
		c, err := app.LoadConfig(*path)
		if err != nil {
			return err
		}
		return console.Run(ctx, c, out)
	}
	if args[0] == "doctor" && len(args) == 2 && args[1] == "--console" {
		c, err := app.LoadConfig(*path)
		if err != nil {
			return err
		}
		report, err := app.Doctor(ctx, c)
		if err != nil {
			return err
		}
		return printJSON(out, report)
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	action := args[0]
	args = args[1:]
	if action == "run" {
		if len(args) != 0 {
			return fmt.Errorf("run 不接受额外参数")
		}
		return control.Run(ctx, c, errOut)
	}
	if action == "doctor" {
		if len(args) != 0 {
			return fmt.Errorf("doctor 不接受额外参数")
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		version, err := codex.Versions(ctx, c)
		if err != nil {
			return err
		}
		p, err := codex.Connect(ctx, c)
		if err != nil {
			return err
		}
		defer p.Close()
		ids, err := codex.Loaded(ctx, p)
		if err != nil {
			return err
		}
		threads := []codex.Thread{}
		for _, id := range ids {
			t, err := codex.Read(ctx, p, id)
			if err != nil {
				return err
			}
			t.Turns = nil
			threads = append(threads, t)
		}
		return printJSON(out, map[string]any{"versions": version, "proxy": "ready", "sessions": threads})
	}
	if action == "status" || action == "sessions" || action == "goals" || action == "tasks" || action == "events" {
		if len(args) != 0 {
			return fmt.Errorf("%s 不接受额外参数", action)
		}
		b, err := control.Call(ctx, c, "/v1/"+action, nil)
		if err != nil {
			return err
		}
		return printJSON(out, b)
	}
	cmd := guard.Command{Action: action}
	switch action {
	case "pause", "resume", "probe":
		if len(args) != 0 {
			return fmt.Errorf("%s 不接受额外参数", action)
		}
	case "goal-retry":
		if len(args) != 1 {
			return fmt.Errorf("goal-retry 需要 ID")
		}
		cmd.ID = args[0]
	case "goal-order":
		if len(args) == 0 {
			return fmt.Errorf("需要全部 Goal ID")
		}
		cmd.Order = args
	default:
		return fmt.Errorf("未知命令 %q\n%s", action, usage)
	}
	b, err := control.Call(ctx, c, "", &cmd)
	if err != nil {
		return err
	}
	return printJSON(out, b)
}
func printJSON(w io.Writer, v any) error {
	if raw, ok := v.(json.RawMessage); ok {
		var b bytes.Buffer
		if err := json.Indent(&b, raw, "", "  "); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w, b.String())
		return err
	}
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(v)
}
