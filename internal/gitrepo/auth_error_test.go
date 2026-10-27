package gitrepo

import (
	"context"
	"strings"
	"testing"
)

func TestAuthHelperExecutionFailureIsNotReportedAsInvalidCredentials(t *testing.T) {
	err := safeGitError(context.Background(), "fatal: cannot run /tmp/ai-pm-git-auth-fixture/askpass: Permission denied\nfatal: could not read Username for 'http://git.example.test': terminal prompts disabled")
	if !strings.Contains(err.Error(), "认证辅助程序无法运行") {
		t.Fatalf("expected server environment error, got %v", err)
	}
	err = safeGitError(context.Background(), "remote: HTTP Basic: Access denied\nfatal: Authentication failed for 'http://git.example.test/repo.git/'")
	if !strings.Contains(err.Error(), "仓库认证失败") {
		t.Fatalf("expected authentication error, got %v", err)
	}
}
