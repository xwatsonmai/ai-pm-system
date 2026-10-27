package server

import (
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"fmt"
	"testing"
)

type driverError int

func (e driverError) Error() string { return fmt.Sprintf("sqlite %d", int(e)) }
func (e driverError) Code() int     { return int(e) }

func TestToolErrorCodeOnlyRetriesTransientFailures(t *testing.T) {
	cases := []struct {
		err       error
		code      string
		retryable bool
	}{
		{domain.ErrRequestReused, "idempotency_conflict", false},
		{fmt.Errorf("%w: 需求修订已更新", domain.ErrConflict), "conflict", false},
		{domain.ErrForbidden, "forbidden", false},
		{fmt.Errorf("读取任务: %w", domain.ErrNotFound), "not_found", false},
		{fmt.Errorf("写入: %w", context.DeadlineExceeded), "unavailable", true},
		{fmt.Errorf("写入: %w", driverError(5)), "unavailable", true},
		{driverError(517), "unavailable", true}, // SQLITE_BUSY_SNAPSHOT
		{driverError(6), "unavailable", true},
		{driverError(19), "invalid_request", false}, // SQLITE_CONSTRAINT
		{errors.New("缺少 body"), "invalid_request", false},
	}
	for _, tc := range cases {
		if code, retryable := toolErrorCode(tc.err); code != tc.code || retryable != tc.retryable {
			t.Errorf("%v: got %s/%v, want %s/%v", tc.err, code, retryable, tc.code, tc.retryable)
		}
	}
}
