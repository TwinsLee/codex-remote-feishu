package wrapper

import (
	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	relayruntime "github.com/kxn/codex-remote-feishu/internal/runtime"
)

func confirmedChildExitEvent(commandID string, session *childSession) agentproto.Event {
	event := agentproto.Event{Kind: agentproto.EventProcessExitReady, CommandID: commandID, Status: "succeeded"}
	if session != nil && session.cmd != nil && session.cmd.Process != nil {
		if err := relayruntime.WaitForProcessExit(session.cmd.Process.Pid, 0); err != nil {
			event.Status = "failed"
			problem := agentproto.ErrorInfoFromError(err, agentproto.ErrorInfo{Code: "child_exit_unconfirmed", Layer: "wrapper", Stage: "shutdown", Operation: string(agentproto.CommandProcessExit), Message: "未确认原生子进程退出，不能确认桌面写锁已释放。", CommandID: commandID})
			event.Problem = &problem
		}
	}
	return event
}
