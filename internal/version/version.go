// Package version holds the build-time injected metadata of godot-ai-cli.
//
// Version, RepoOwner and RepoName are overridden at release time via
// -ldflags "-X ...". The Godot compatibility constants mirror the
// upstream godot-ai plugin support policy (Godot 4.5+, 4.7 recommended).
package version

var (
	// Version is the CLI version string (semver). Overridden by goreleaser-style ldflags.
	// The build-in default is 0.0.0-dev so an unstamped dev build compares OLDER
	// than any published release — including pre-releases (semver §11 ranks
	// 0.1.0-dev above 0.1.0-beta.1, which would wrongly suppress the update offer).
	Version = "0.0.0-dev"
	// RepoOwner is the GitHub account that hosts the release repository.
	RepoOwner = "mimajiushi"
	// RepoName is the GitHub repository that publishes release assets.
	RepoName = "godot-ai-cli"
)

const (
	// ProtocolVersion is the editor-plugin wire protocol version implemented here.
	ProtocolVersion = 1
	// SupportedGodotMin is the minimum supported Godot version (inclusive).
	SupportedGodotMin = "4.5"
	// SupportedGodotRecommended is the minimum recommended Godot version.
	SupportedGodotRecommended = "4.7"
)
