//go:build !darwin

package holder

// handoff: only macOS judges a pane's programs by their holder's code
// (handoff.go), so elsewhere a holder runs on as it is through an update.
type handoff struct{}

func (*handoff) init(*Server)             {}
func (*handoff) watch(*Server)            {}
func (*handoff) awaitOutput(*Server) bool { return true }
func (*handoff) readerEnded()             {}
