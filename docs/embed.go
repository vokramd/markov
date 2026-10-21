// Package docs embeds the reference documentation, so tools built into the markov binary (the
// codification retro agent) can hand it to an agent wherever markov is installed.
package docs

import "embed"

// Reference holds docs/reference/*.md.
//
//go:embed reference/*.md
var Reference embed.FS
