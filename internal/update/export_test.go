// Test seams: helpers only test code uses, kept out of the production binary.
package update

// CompareSemver compares two semver-ish release tags.
func CompareSemver(left string, right string) (int, error) {
	leftParts, err := parseSemver(left)
	if err != nil {
		return 0, err
	}
	rightParts, err := parseSemver(right)
	if err != nil {
		return 0, err
	}
	return compareSemverParts(leftParts, rightParts), nil
}

// NormalizeVersionTag returns a comparable x.y.z version from a release tag.
func NormalizeVersionTag(version string) (string, error) {
	return normalizeVersionTag(version)
}

// ResolveEndpoint resolves a URL or owner/repo slug into a release API endpoint.
func ResolveEndpoint(endpointOrRepository string, repository string) (string, error) {
	return resolveEndpoint(endpointOrRepository, repository)
}
func parseSemver(version string) (semverParts, error) {
	normalized, err := NormalizeVersionTag(version)
	if err != nil {
		return semverParts{}, err
	}
	return parseSemverNormalized(normalized)
}
