package view

import "testing"

// The self-refreshing pages must opt into Resumable, or their tick chain dies the
// first time the user drills in and comes back — leaving a table that looks live
// but never updates again.
func TestSelfRefreshingPagesAreResumable(t *testing.T) {
	for name, p := range map[string]any{
		"crdBrowsePage": &crdBrowsePage{},
		"nodeTopPage":   &nodeTopPage{},
		"tenantsPage":   &tenantsPage{},
	} {
		if _, ok := p.(Resumable); !ok {
			t.Errorf("%s drives its own tick but does not implement Resumable", name)
		}
	}
}

func TestCRDBrowseResumeRearmsAndInvalidatesOldTicks(t *testing.T) {
	p := &crdBrowsePage{token: 7}
	cmd := p.OnResume()
	if cmd == nil {
		t.Fatal("OnResume returned no command, so nothing re-arms")
	}
	if p.token == 7 {
		t.Error("OnResume kept the old token; an in-flight tick from before the drill-in could double the chain")
	}
	// A tick carrying the pre-resume token must still be ignored.
	if _, _ = p.Update(crdTickMsg{token: 7}); p.token == 7 {
		t.Error("stale tick was not rejected")
	}
}
