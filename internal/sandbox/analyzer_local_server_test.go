package sandbox

import "testing"

// LocalServer exists to say one narrow thing: this command BINDS a local port,
// so it is not egress and should not be treated as network access. That claim
// is only worth anything if it tracks what the command actually does.
//
// Matching on the program name alone made every invocation of a framework CLI a
// "server", so `next build` and `vite build` claimed to bind a port while
// compiling to disk. Nothing reads the flag today, which is exactly why it could
// be wrong quietly; the first reader would have inherited the bug.
func TestLocalServerDistinguishesServingFromBuilding(t *testing.T) {
	serving := []string{
		"next dev", "next start", "nuxt dev", "astro dev", "astro preview",
		"vite", "vite serve", "vite preview",
		// Options only, no subcommand. firstSubcommand skips the flag but not the
		// value it consumes, so this used to resolve to "127.0.0.1" and read as a
		// build.
		"vite --host 127.0.0.1", "vite --port 5173",
		// Dedicated servers, whose entire purpose is to bind.
		"http-server", "http-server ./public", "serve", "serve dist",
		// Via a package manager, the pre-existing path.
		"npm run dev", "pnpm start", "yarn preview",
	}
	for _, command := range serving {
		if !AnalyzeCommand(command).LocalServer {
			t.Errorf("%q does bind a local port but was not classified as a local server", command)
		}
	}

	building := []string{
		"next build", "next lint", "next",
		"vite build", "vite optimize",
		"nuxt generate", "nuxt build",
		"astro check", "astro build",
		"npm run build",
	}
	for _, command := range building {
		if AnalyzeCommand(command).LocalServer {
			t.Errorf("%q compiles rather than serving but was classified as a local server", command)
		}
	}
}

// The point of the distinction is that binding is not egress, so neither form
// may be mistaken for network access. A build that genuinely fetches is caught
// by the network rules for its own program, not by this flag.
func TestNeitherServingNorBuildingCountsAsNetworkEgress(t *testing.T) {
	for _, command := range []string{"next dev", "next build", "vite", "vite build", "http-server"} {
		if AnalyzeCommand(command).Network {
			t.Errorf("%q was classified as network egress; binding or compiling locally is neither", command)
		}
	}
}
