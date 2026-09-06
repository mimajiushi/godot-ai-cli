package daemonctl

// StubSpawnForTest replaces the detached-spawn function for one test and
// returns a restore function. Compiled into tests only.
func StubSpawnForTest(fn func(httpPort, wsPort int) error) (restore func()) {
	orig := spawnServe
	spawnServe = fn
	return func() { spawnServe = orig }
}

// VersionCompatibleForTest exposes the adoption version gate to tests.
func VersionCompatibleForTest(running, requested string) bool {
	return versionCompatible(running, requested)
}
