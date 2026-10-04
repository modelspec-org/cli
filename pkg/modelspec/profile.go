package modelspec

import "fmt"

// Profile selects the rules Check applies.
//
// The default profile implements the standard as written in the ModelSpec
// repository (spec/core-model.md, spec/hcl-authoring.md, spec/json-format.md and
// the decisions) and nothing else: a model the standard allows is never refused
// because some consumer's reader is narrower. The publish profile adds what a
// model needs to be listed in public catalogues, which is what the Directory's
// JSON reader requires on top of the standard.
type Profile string

const (
	ProfileDefault Profile = "default"
	ProfilePublish Profile = "publish"
)

// Profiles lists the profile names.
var Profiles = []Profile{ProfileDefault, ProfilePublish}

// ParseProfile converts a flag value to a Profile.
func ParseProfile(s string) (Profile, error) {
	for _, p := range Profiles {
		if string(p) == s {
			return p, nil
		}
	}
	return "", fmt.Errorf("unknown profile %q: expected default or publish", s)
}

// Options configures Check.
type Options struct {
	Profile Profile // "" means ProfileDefault
	// Unread names the modules that have a file which was found and not read (Lint's
	// skipped files). A module of that name that is not among the models is known and
	// cannot be checked, so a reference into it is not reported as an unknown module.
	Unread []string
}

func (o Options) publish() bool { return o.Profile == ProfilePublish }
