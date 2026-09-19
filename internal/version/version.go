// Package version holds the build-time injected metadata of godot-ai-cli.
//
// Version, RepoOwner and RepoName are overridden at release time via
// -ldflags "-X ...". The Godot compatibility constants mirror the
// upstream godot-ai v4 support policy (Godot 4.7+ required; 5.x refused).
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
	// ProtocolVersion is the editor-plugin WebSocket wire protocol version
	// implemented here (v4 authenticated transport; mirrors bridge.WSProtocolVersion).
	ProtocolVersion = 2
	// SupportedGodotMin is the minimum supported Godot version (inclusive).
	// 上游 v4 插件的 _supports_v4_editor 只放行 4.7+ 的 4.x 线。
	SupportedGodotMin = "4.7"
	// SupportedGodotRecommended is the minimum recommended Godot version.
	SupportedGodotRecommended = "4.7"
)
