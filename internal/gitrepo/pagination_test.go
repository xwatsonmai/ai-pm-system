package gitrepo

import (
	"ai-pm-system/internal/domain"
	"fmt"
	"strings"
	"testing"
)

func TestLinePageDoesNotTruncateLaterRowsAndBoundsLongLines(t *testing.T) {
	writer := &linePage{page: domain.PageQuery{Page: 36, PageSize: 20}, items: []string{}}
	for i := 0; i < 725; i++ {
		if _, e := fmt.Fprintf(writer, "file-%04d.go\n", i); e != nil {
			t.Fatal(e)
		}
	}
	writer.finish()
	if writer.total != 725 || len(writer.items) != 20 || writer.items[0] != "file-0700.go" || writer.items[19] != "file-0719.go" {
		t.Fatal(writer)
	}
	writer = &linePage{page: domain.PageQuery{Page: 1, PageSize: 20}, items: []string{}}
	writer.Write([]byte(strings.Repeat("x", 200000)))
	writer.Write([]byte("\nlast"))
	writer.finish()
	if writer.total != 2 || len(writer.items[0]) != 65536 || writer.items[1] != "last" || !writer.truncated {
		t.Fatal("long line pagination broken")
	}
}
