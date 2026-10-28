package main

import (
	"ai-pm-system/internal/notify"
	"ai-pm-system/internal/notify/wecom"
)

// IM channels available in the platform settings. A private build can add its
// own in another file of this package.
func init() {
	notify.Register("wecom", wecom.Spec())
}
