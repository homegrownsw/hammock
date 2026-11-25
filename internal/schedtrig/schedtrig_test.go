package schedtrig

import "testing"

// GitHub commit and release triggers, in the folder's repository or one
// named after @, read back as written.
func TestParseGitHubCommitAndRelease(t *testing.T) {
	for spec, want := range map[string]string{
		"github:commit":                             "A new commit on the default branch",
		"github:commit=dev":                         "A new commit on dev",
		"github:commit@mitchellh/go-libghostty":     "A new commit on the default branch (mitchellh/go-libghostty)",
		"github:release":                            "A new release or tag",
		"github:release=vt-*@ghostty-org/ghostty":   "A new release or tag matching vt-* (ghostty-org/ghostty)",
		"github:label=needs triage@acme/widgets":    "An issue or pull request is labelled “needs triage” (acme/widgets)",
		"github:label=me@example.com":               "An issue or pull request is labelled “me@example.com”",
		"gh:merged@https://github.com/acme/widgets": "My pull request is merged (acme/widgets)",
	} {
		tr, err := Parse(spec, nil)
		if err != nil {
			t.Fatalf("%s: %v", spec, err)
		}
		if got := Describe(tr, nil); got != want {
			t.Errorf("%s: %q, want %q", spec, got, want)
		}
		again, err := Parse(Spec(tr), nil)
		if err != nil || Spec(again) != Spec(tr) {
			t.Errorf("%s: written as %q, read back as %q (%v)", spec, Spec(tr), Spec(again), err)
		}
	}
	if _, err := Parse("github:release=[v", nil); err == nil {
		t.Error("a bad tag pattern was taken")
	}
}

func TestGitHubRepo(t *testing.T) {
	for in, want := range map[string]string{
		"acme/widgets":                            "acme/widgets",
		"https://github.com/acme/widgets.git":     "acme/widgets",
		"git@github.com:acme/widgets.git":         "acme/widgets",
		"ssh://git@github.com/acme/widgets":       "acme/widgets",
		"https://gitlab.com/acme/widgets":         "",
		"acme":                                    "",
		"acme/widgets/tree/main":                  "",
		"tangled.org/mitchellh.com/go-libghostty": "",
	} {
		if got := GitHubRepo(in); got != want {
			t.Errorf("GitHubRepo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTagMatchesAndCooldownWords(t *testing.T) {
	if !TagMatches("", "anything") || !TagMatches("vt-*", "vt-1.0.2") || TagMatches("vt-*", "v1.0") {
		t.Error("TagMatches")
	}
	for secs, want := range map[int]string{86400: "1 day cooldown", 604800: "1 week cooldown", 2592000: "1 month cooldown", 3600: "1 hour cooldown"} {
		if got := CooldownWords(secs); got != want {
			t.Errorf("CooldownWords(%d) = %q, want %q", secs, got, want)
		}
	}
}
