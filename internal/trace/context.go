package trace

import "context"

type ctxKey struct{}

// WithContext returns a copy of ctx that carries r, so the providerio seam
// and other lower layers can reach the recorder without changing their
// function signatures. Passing a nil recorder still injects a value (a nil
// *Recorder), but FromContext returns nil for it so downstream no-op guards
// see nil.
func WithContext(ctx context.Context, r *Recorder) context.Context {
	return context.WithValue(ctx, ctxKey{}, r)
}

// FromContext returns the recorder carried by ctx, or nil if none is present
// (or if the carried value is nil). Callers must guard all stamps with a nil
// check; the recorder methods are themselves nil-safe.
func FromContext(ctx context.Context) *Recorder {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(ctxKey{}).(*Recorder)
	return v
}
