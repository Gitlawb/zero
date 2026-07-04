package tools

import (
	"context"
	"errors"
)

func searchCancelledResult(tool string, err error) (Result, bool) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Result{Status: StatusError, Output: "Error: " + tool + " cancelled."}, true
	}
	return Result{}, false
}
