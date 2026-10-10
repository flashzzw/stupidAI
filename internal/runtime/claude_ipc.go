package runtime

import (
	"context"
	"errors"
	"syscall"
	"time"
)

// Fixed Claude 2.1.280 writes stdio MCP initialize through libc send on an
// anonymous Unix socket. Some launch environments allow socketpair/write but
// deny send. Probe that operation without binding, launching a server or
// changing how a rejected native call is executed.
func checkClaudeLocalIPC(ctx context.Context) *LocalIPCCheck {
	if err := ctx.Err(); err != nil {
		return localIPCResult("cancelled", err)
	}
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return localIPCResult("socketpair", err)
	}
	defer syscall.Close(fds[0])
	defer syscall.Close(fds[1])
	err = syscall.Sendto(fds[0], []byte{0}, syscall.MSG_DONTWAIT|syscall.MSG_NOSIGNAL, nil)
	return localIPCResult("send", err)
}

func localIPCResult(operation string, err error) *LocalIPCCheck {
	out := &LocalIPCCheck{Status: "passed", Operation: operation, At: time.Now().UTC().Format(time.RFC3339Nano), Detail: "本工作台进程的匿名 Unix socket send 写入检查通过；未连接 MCP 服务或验证认证、工具权限。"}
	if err == nil {
		return out
	}
	out.Status = "unverified"
	out.Detail = "本工作台进程的本地 IPC 环境检查未完成；不能据此判断 MCP 服务是否可用。"
	switch {
	case errors.Is(err, syscall.EPERM):
		out.ErrorCode = "EPERM"
	case errors.Is(err, syscall.EACCES):
		out.ErrorCode = "EACCES"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		out.ErrorCode = "CANCELLED"
	default:
		out.ErrorCode = "CHECK_FAILED"
	}
	if out.ErrorCode == "EPERM" || out.ErrorCode == "EACCES" {
		out.Status = "blocked"
		out.Detail = "本工作台进程的匿名 Unix socket " + operation + " 被拒绝（" + out.ErrorCode + "）。固定 Claude stdio MCP 需要本地 IPC 写入；请检查启动工作台时的环境限制。"
	}
	return out
}
