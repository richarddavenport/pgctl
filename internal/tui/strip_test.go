package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ansi.Strip is scaffolding, and this is what stops it becoming furniture.
//
// The canvas draws clusters into cells, so an escape sequence handed to it is
// text rather than styling — a styled string reaches the frame as a blank row,
// with colour ON and not with colour off, which is why no golden can see it.
// Stripping keeps the SHAPE exactly right and loses only the colour, so it is
// the honest way to port one screen at a time.
//
// It is honest only while it is temporary. The pane's fourteen tabs are done —
// every one is a comp.Detail or a comp.Table — and the overlays are the last
// thing still building styled strings. When drawAction and drawHelp become
// comp.Form, comp.Confirm and comp.Keys, this test fails, and the fix is to
// delete it along with the import.
//
// An assertion rather than a probe because it ratchets both ways: it fails if
// somebody adds a strip somewhere new, and it fails once the last one goes.
func TestOnlyTheOverlaysStillStripColour(t *testing.T) {
	const expected = "actionview.go"

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatal(err)
		}
		// Counted as a call, not as the word: the comments above each one
		// explain what it is for and would otherwise count themselves.
		if strings.Contains(string(src), "ansi.Strip(") {
			found = append(found, name)
		}
	}

	switch {
	case len(found) == 0:
		t.Errorf("nothing strips colour any more — delete this test and the "+
			"ansi import in %s", expected)
	case len(found) == 1 && found[0] == expected:
		// The known remainder: the modal and the ? overlay.
	default:
		t.Errorf("colour is stripped in %v; only %s should, and only until the "+
			"overlays are comp.Form, comp.Confirm and comp.Keys", found, expected)
	}
}
