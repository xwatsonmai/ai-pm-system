package agent

import (
	"ai-pm-system/internal/domain"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFinishRunOutputRetainsEarlierVisibleText(t *testing.T) {
	for _, test := range []struct{ streamed, final, process string }{
		{"检查资料。最终结论。", "最终结论。", "检查资料。"},
		{"最终结论。", "最终结论。", ""},
		{"最终", "最终结论。", ""},
		{"已显示的说明。", "另一个最终回答。", "已显示的说明。"},
	} {
		r := domain.Run{Output: test.streamed}
		finishRunOutput(&r, test.final)
		if r.Output != test.final || r.ProcessOutput != test.process {
			t.Fatalf("lost text: %+v", r)
		}
		finishRunOutput(&r, test.final)
		if r.ProcessOutput != test.process {
			t.Fatal("completion duplicated process text")
		}
	}
	r := domain.Run{ProcessOutput: "前一轮说明", Output: "停止前的部分回答"}
	finishRunOutput(&r, "")
	if r.Output != "停止前的部分回答" || r.ProcessOutput != "前一轮说明" {
		t.Fatal("empty completion discarded partial output")
	}
}

func TestRunProcessLimitPreservesUTF8(t *testing.T) {
	r := domain.Run{}
	appendRunProcess(&r, strings.Repeat("文🙂", 40000))
	if len(r.ProcessOutput) > 200000 || !utf8.ValidString(r.ProcessOutput) {
		t.Fatal("invalid bounded process text")
	}
	appendRunProcess(&r, "新增")
	if len(r.ProcessOutput) > 200000 || !utf8.ValidString(r.ProcessOutput) {
		t.Fatal("limit exceeded")
	}
}
