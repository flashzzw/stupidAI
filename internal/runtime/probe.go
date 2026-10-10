package runtime

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DateCommand makes the command's own exit status observable even on SDKs
// whose Bash result does not include an exit-code field.
const DateCommand = `netcap_probe_utc=$(/usr/bin/date -u +%FT%TZ); netcap_probe_exit=$?; printf '__NETCAP_UTC__%s %s\n' "$netcap_probe_exit" "$netcap_probe_utc"; exit "$netcap_probe_exit"`

func ProbePrompt(mode string) string {
	if mode == "text" {
		return "Return exactly NETCAP_GENERATION_OK without calling any tools."
	}
	return "Run exactly this command once with the Bash/shell tool, without adding or changing any shell statement. Then return only the UTC timestamp after the exit code, with no other text.\n" + DateCommand
}

// VerifyProbe requires a terminal native turn and matching native tool output;
// model-generated text alone is never proof that a command ran.
func VerifyProbe(view View, turn, mode string, started time.Time) (string, error) {
	if !view.Evidence.Complete || !view.ExecutionClear {
		return "", fmt.Errorf("原生执行范围尚未核对完整")
	}
	finished := false
	for _, t := range view.Turns {
		if t.ID == turn && t.Status == "completed" {
			finished = true
		}
	}
	if !finished {
		return "", fmt.Errorf("原生轮次尚未成功结束")
	}
	final := ""
	for _, message := range view.Messages {
		if message.TurnID == turn && message.Role == "assistant" && message.Status != "inProgress" {
			final = strings.TrimSpace(message.Text)
		}
	}
	if mode == "text" {
		for _, tool := range view.Tools {
			if tool.TurnID == turn {
				return "", fmt.Errorf("纯响应探测出现了工具调用")
			}
		}
		if final != "NETCAP_GENERATION_OK" {
			return "", fmt.Errorf("生成能力探测响应不匹配")
		}
		return final, nil
	}
	if mode != "utc-date" {
		return "", fmt.Errorf("探测模式无效")
	}
	count := 0
	for _, tool := range view.Tools {
		if tool.TurnID == turn {
			count++
			if tool.Command != DateCommand {
				return "", fmt.Errorf("探测执行了固定日期命令之外的工具")
			}
		}
	}
	if count != 1 {
		return "", fmt.Errorf("日期探测需要且只接受一次原生工具调用")
	}
	for _, tool := range view.Tools {
		if tool.TurnID != turn || tool.Command != DateCommand || !tool.Complete {
			continue
		}
		if tool.ExitCode != nil && *tool.ExitCode != 0 {
			return "", fmt.Errorf("日期工具退出码为 %d", *tool.ExitCode)
		}
		for _, line := range strings.Split(strings.TrimSpace(tool.Output), "\n") {
			body, ok := strings.CutPrefix(line, "__NETCAP_UTC__")
			if !ok {
				continue
			}
			code, timestamp, ok := strings.Cut(body, " ")
			exit, err := strconv.Atoi(code)
			if !ok || err != nil || exit != 0 {
				return "", fmt.Errorf("日期命令未以退出码 0 完成")
			}
			at, err := time.Parse("2006-01-02T15:04:05Z", timestamp)
			if err != nil || at.Before(started.Add(-2*time.Second)) || at.After(time.Now().UTC().Add(2*time.Second)) {
				return "", fmt.Errorf("日期输出不是本次执行期间的 UTC 时间")
			}
			if final != timestamp {
				return "", fmt.Errorf("最终响应与原生日期输出不一致")
			}
			return timestamp, nil
		}
	}
	return "", fmt.Errorf("缺少本次日期命令、退出码和原生工具输出证据")
}

// execCommand recognizes only a single JSON argument call. It does not
// evaluate JavaScript or treat arbitrary printed objects as tool evidence.
func execCommand(code string) (string, bool) {
	code = strings.TrimSpace(code)
	body, ok := strings.CutPrefix(code, "text(await tools.exec_command(")
	if !ok {
		return "", false
	}
	body, ok = strings.CutSuffix(body, "));")
	if !ok {
		return "", false
	}
	var args map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &args) != nil {
		return "", false
	}
	for key := range args {
		if key != "cmd" && key != "login" && key != "yield_time_ms" && key != "max_output_tokens" {
			return "", false
		}
	}
	var command string
	if json.Unmarshal(args["cmd"], &command) != nil {
		return "", false
	}
	return command, true
}
