package main

import "github.com/Gitlawb/zero-fixture/internal/release"

func smokeTarget(alreadyBuilt bool) string {
	return release.SmokeTarget(alreadyBuilt)
}

func main() {}
