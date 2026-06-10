package repoinfo

import (
	"sort"
	"strings"
)

// buildToolFiles are basenames that indicate a build system.
var buildToolFiles = map[string]bool{
	"Makefile": true, "go.mod": true, "Cargo.toml": true, "package.json": true,
	"pyproject.toml": true, "setup.py": true, "pom.xml": true,
	"build.gradle": true, "build.gradle.kts": true, "CMakeLists.txt": true,
	"Gemfile": true, "composer.json": true,
}

// testToolFiles are basenames that indicate a test framework/config.
var testToolFiles = map[string]bool{
	"jest.config.js": true, "jest.config.ts": true, "jest.config.mjs": true,
	"vitest.config.ts": true, "vitest.config.js": true,
	"pytest.ini": true, "tox.ini": true, "phpunit.xml": true,
	".mocharc.json": true, ".mocharc.js": true, ".mocharc.yml": true,
	"playwright.config.ts": true, "playwright.config.js": true, "karma.conf.js": true,
}

// workspaceMarkers maps a marker basename to a monorepo workspace type.
var workspaceMarkers = map[string]string{
	"pnpm-workspace.yaml": "pnpm",
	"go.work":             "go-work",
	"lerna.json":          "lerna",
	"nx.json":             "nx",
}

// cicdForPath returns a CI/CD system name for a repo-relative path, or "".
func cicdForPath(filePath string) string {
	switch {
	case strings.HasPrefix(filePath, ".github/workflows/"):
		return "GitHub Actions"
	case filePath == ".gitlab-ci.yml":
		return "GitLab CI"
	case filePath == ".circleci/config.yml":
		return "CircleCI"
	case filePath == "Jenkinsfile":
		return "Jenkins"
	case filePath == "azure-pipelines.yml":
		return "Azure Pipelines"
	case filePath == ".travis.yml":
		return "Travis CI"
	case filePath == "bitbucket-pipelines.yml":
		return "Bitbucket Pipelines"
	}
	return ""
}

// sortedUnique returns the keys of set as a sorted slice (never nil).
func sortedUnique(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
