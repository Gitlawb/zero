#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

export GOCACHE="${GOCACHE:-/tmp/zero-go-cache}"

go test ./internal/sandbox -run 'TestPermissionProfileFromPolicyBuildsWorkspaceWriteProfile|TestPermissionProfileFromDisabledPolicyDoesNotRequirePlatformSandbox|TestSandboxManagerBuildsExecutionRequestFromProfile|TestSandboxManagerBuildsCommandPlanThroughTemporaryAdapter|TestSandboxManagerBuildsDegradedPolicyOnlyCommandPlan|TestSandboxManagerSelectsPlatformBackend|TestSelectBackendDelegatesToSandboxManagerSelection|TestSandboxManagerFailsClosedWhenNativeRequiredAndPolicyOnlyDisabled|TestBuildLinuxSandboxCommandArgsSerializesPermissionProfile|TestParseLinuxSandboxHelperArgs|TestRunLinuxSandboxHelperFailsClosedUntilEnforcementIsWired|TestTargetBackendForPlatformBaseline|TestLegacySandboxEntrypointsAreExplicitAndExist|TestBackendPlanCarriesPhase0ManagerFields|TestCommandPlanCarriesSandboxMetadata|TestPolicyOnlyDisabledFailClosedForTargetPlatforms|TestSelectBackendChoosesPlatformAdapterWithFallback|TestBackendBuildPlanDocumentsBestEffortIsolation|TestBackendCapabilitiesReflectDisabledPolicy'
go test ./internal/cli -run 'TestRunSandboxPolicyInspectTextAndJSON|TestRunSandboxPolicyJSONGoldenIncludesManagerBaselineFields|TestRunSandboxPolicyEffectiveTextAndJSON'
go test ./internal/tools -run 'TestRegistryRunsBashThroughSandboxEngine|TestBashToolReportsDisabledPolicyOnlyRunner|TestBashToolBuildsWrappedSandboxExecCommand'

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT

compile_pkg() {
  local goos="$1"
  local pkg="$2"
  local name
  name="$(basename "$pkg")"
  GOOS="$goos" GOARCH=amd64 go test -c -o "$tmpdir/${goos}-${name}.test" "$pkg"
}

for goos in linux darwin windows; do
  compile_pkg "$goos" ./internal/sandbox
  compile_pkg "$goos" ./internal/cli
  compile_pkg "$goos" ./cmd/zero
done
compile_pkg linux ./cmd/zero-seccomp
compile_pkg linux ./cmd/zero-linux-sandbox

case "$(go env GOOS)" in
  linux)
    ZERO_SANDBOX_REAL_SMOKE=1 go test ./internal/sandbox -run TestBubblewrapRealSandboxSmoke -count=1
    ;;
  darwin)
    go test ./internal/sandbox -run TestSandboxExecProfileAllowsDevNullAndTemp -count=1
    ;;
  windows)
    echo "Windows real sandbox smoke will be enabled with the native Windows backend."
    ;;
  *)
    echo "No real sandbox smoke is defined for this host platform."
    ;;
esac
