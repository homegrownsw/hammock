package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/homegrownsw/hammock/internal/acknowledgements"
)

// runAcknowledgements is `hmk acknowledgements [NAME | --all]`: the
// third-party software in this build of Hammock (the Mac app's on a Mac, the
// Linux build's elsewhere) and its licences. The apps show the same list in
// Settings ▸ About.
func runAcknowledgements(args []string) error {
	cs := acknowledgements.For(acknowledgements.ThisApp())
	switch {
	case len(args) == 0:
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for _, c := range cs {
			fmt.Fprintf(w, "%s\t%s\n", c.Heading(), c.License)
		}
		w.Flush()
		fmt.Println("\nhmk acknowledgements NAME shows one's licence; --all shows every one.")
		return nil
	case args[0] == "--all":
		for i, c := range cs {
			if i > 0 {
				fmt.Print("\n" + strings.Repeat("=", 72) + "\n\n")
			}
			printNotice(c)
		}
		return nil
	}
	found := acknowledgements.Find(cs, strings.Join(args, " "))
	if len(found) == 0 {
		return fmt.Errorf("no component called %q (hmk acknowledgements lists them)", strings.Join(args, " "))
	}
	for i, c := range found {
		if i > 0 {
			fmt.Print("\n" + strings.Repeat("=", 72) + "\n\n")
		}
		printNotice(c)
	}
	return nil
}

func printNotice(c acknowledgements.Component) {
	fmt.Print(c.Notice())
	if c.Credits == "chromium" {
		if p := chromiumCredits(); p != "" {
			fmt.Printf("\nChromium's credits: %s\n", p)
		}
	}
}

// chromiumCredits is the Chromium credits page in the Mac app's browser host,
// beside this hmk (Contents/Helpers), if it's there.
func chromiumCredits() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe, _ = filepath.EvalSymlinks(exe)
	p := filepath.Join(filepath.Dir(exe), "Hammock Browser.app/Contents/Resources/Chromium Credits.html")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}
