package heimdal

import "embed"

// SkillFiles contains the skill shipped with Heimdal so the binary can install
// it without depending on the source checkout being present.
//
//go:embed skills/heimdal-playwright-qa/**
var SkillFiles embed.FS
