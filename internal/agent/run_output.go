package agent

import (
	"ai-pm-system/internal/domain"
	"strings"
	"unicode/utf8"
)

func appendRunProcess(r *domain.Run, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if r.ProcessOutput != "" {
		text = "\n\n" + text
	}
	remaining := 200000 - len(r.ProcessOutput)
	if remaining <= 0 {
		return
	}
	if len(text) > remaining {
		text = text[:remaining]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	r.ProcessOutput += text
}

func finishRunOutput(r *domain.Run, final string) {
	if final == "" || final == r.Output {
		return
	}
	// Also accept workers without message boundaries. Their accumulated text
	// ends with the final answer; retain only the earlier visible explanation.
	if !strings.HasPrefix(final, r.Output) {
		previous := strings.TrimSuffix(r.Output, final)
		appendRunProcess(r, previous)
	}
	r.Output = final
	r.OutputVersion++
}
