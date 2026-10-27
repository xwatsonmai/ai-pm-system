package agent

import (
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/sandbox"
	"context"
	"io"
	"os/exec"
	"path/filepath"
)

type workerProcess struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	wait   func() error
	kill   func() error
}

func (m *Manager) startWorker(ctx context.Context, r domain.Run, stderr io.Writer) (*workerProcess, error) {
	if r.CodeSnapshot != nil {
		conn, reader, err := sandbox.Connect(ctx, m.SandboxSocket, sandbox.Request{ProjectID: r.ProjectID, ConversationID: r.ConversationID, RunID: r.ID, SnapshotKey: r.CodeSnapshot.Key})
		if err != nil {
			return nil, err
		}
		return &workerProcess{stdin: conn, stdout: io.NopCloser(reader), wait: conn.Close, kill: conn.Close}, nil
	}
	cmd := exec.Command("node", m.Worker)
	cmd.Dir = filepath.Dir(m.Worker)
	cmd.Stderr = stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		return nil, err
	}
	return &workerProcess{stdin: in, stdout: out, wait: cmd.Wait, kill: cmd.Process.Kill}, nil
}

func readOnlyTool(name string) bool {
	return name == "requirement_iterations" || name == "project_milestone_get" || name == "project_timeline" || name == "project_version_list" || name == "project_version_get_context" || name == "requirement_pool" || name == "requirement_detail" || name == "project_context" || name == "repository_read" || name == "repository_info" || name == "repository_history"
}

func nativeTool(name string) bool {
	return name == "read" || name == "grep" || name == "find" || name == "ls"
}
