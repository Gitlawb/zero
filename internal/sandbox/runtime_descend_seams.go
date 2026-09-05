package sandbox

// The two seams the rooted descent exposes, shared by both platform
// implementations so a test asserts the same property on either.
//
// They live apart from the Windows descent they started in because the POSIX
// fallback now takes the same shape, and a seam defined next to one of two
// implementations is how the other one quietly ends up untested.

// runtimeDescentBarrier, when set, runs after the base directory has been opened
// and before the first owned component is touched. It exists so a test can swap
// an owned component for a link at exactly the point the old pathname walk was
// vulnerable, and prove the redirected target is never created or granted. Nil in
// production.
var runtimeDescentBarrier func()

// runtimeBaseOpenedByName, when set, receives the ONE path this descent opens by
// name. The whole security property is which path that is: the fixed cache or
// temp directory above the owned tail, never a predictable component Zero owns. A
// test can assert it directly instead of inferring it from whether a swap
// happened to be caught, which is not discriminating. Nil in production.
var runtimeBaseOpenedByName func(string)
