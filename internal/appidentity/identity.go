// Package appidentity centralizes the application identity policy: new writes
// use "groundskeeper", legacy "agent-deck" paths are read-only fallbacks.
// This is the single source of truth for which app name to use for new
// directories, config files, and display strings.
package appidentity

// AppName is the current product name.
const AppName = "groundskeeper"

// LegacyName is the upstream product name (Agent Deck). Legacy paths under
// this name are read-only migration sources.
const LegacyName = "agent-deck"

// LegacyDotDir is the legacy hidden directory name (~/.agent-deck).
const LegacyDotDir = ".agent-deck"

// IsLegacy reports whether a path component is the legacy app name.
func IsLegacy(name string) bool { return name == LegacyName || name == LegacyDotDir }

// ShouldUseLegacy reports whether a legacy path should be used as a fallback
// (read-only). This returns true for migration-source paths and false for new
// writes.
func ShouldUseLegacy(path string) bool {
	return path == LegacyName || path == LegacyDotDir
}
