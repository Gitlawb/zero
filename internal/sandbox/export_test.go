package sandbox

// ZeroUserConfigDir exports zeroUserConfigDir for parity tests against
// config.UserConfigDir. Production callers stay on the unexported helper to
// avoid growing the sandbox public surface.
var ZeroUserConfigDir = zeroUserConfigDir
