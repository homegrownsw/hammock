package main

import (
	"fmt"
	"os/exec"
	"runtime"
)

// focusInApp brings the Mac app forward on a pane: it opens
// hammock://pane/<id>, which the app answers by selecting the pane's tab and
// focusing it (AppModel.reveal), the same as clicking its notification. A
// tab reveals its focused pane. Full ids only go in the URL, so an
// abbreviation never reaches the app.
func focusInApp(w *world, ref string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("hmk focus needs the Mac app")
	}
	pid, err := w.resolve(ref, "pane")
	if err != nil {
		tid, terr := w.resolve(ref, "tab")
		if terr != nil {
			return err
		}
		t := w.tabs[tid]
		if t == nil || t.FocusedPaneId == "" {
			return fmt.Errorf("tab %s has no focused pane", tid)
		}
		pid = t.FocusedPaneId
	}
	if out, err := exec.Command("/usr/bin/open", "hammock://pane/"+pid).CombinedOutput(); err != nil {
		return fmt.Errorf("opening the Mac app: %v %s", err, out)
	}
	return nil
}
