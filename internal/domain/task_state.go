package domain

func TaskActive(status string) bool {
	switch status {
	case "clarify", "ready", "doing", "aligning", "testing", "fixing", "retest", "verifying", "pending_release", "review":
		return true
	}
	return false
}

type DevelopmentAlignment struct {
	Status string `json:"status"` // waiting_product, replied, completed
}

func TaskTerminated(status string) bool {
	return status == "closed" || status == "rejected" || status == "canceled"
}

func TaskFinished(status string) bool {
	return status == "done" || status == "resolved" || TaskTerminated(status)
}

type TaskResolution struct {
	FromStatus string `json:"from_status"`
	Reason     string `json:"reason"`
	Actor      string `json:"actor"`
	At         int64  `json:"at"`
}

// UserInstruction is an explicit-authorization assertion supplied by the MCP
// client. UserID and ActorID come from the authenticated connection, not tool args.
type TaskActionAuthorization struct {
	Source          string `json:"source"`
	UserID          string `json:"user_id"`
	ActorID         string `json:"actor_id"`
	UserConfirmed   bool   `json:"user_confirmed"`
	UserInstruction string `json:"user_instruction"`
}
