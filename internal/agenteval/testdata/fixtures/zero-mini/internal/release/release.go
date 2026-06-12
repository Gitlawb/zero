package release

func SmokeTarget(alreadyBuilt bool) string {
	if alreadyBuilt {
		return "local-binary"
	}
	return "build-then-smoke"
}
