package skills

import "embed"

// FS embeds the bundled Groundskeeper skills for first-run setup installs.
//
//go:embed */SKILL.md
var FS embed.FS
