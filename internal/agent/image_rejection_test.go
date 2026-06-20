package agent

import (
	"errors"
	"testing"
)

func TestIsImageRejectionError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "400 with image mention",
			err:  errors.New("HTTP 400: image content is not supported by this model"),
			want: true,
		},
		{
			name: "400 with multimodal mention",
			err:  errors.New("400 Bad Request: model does not support multimodal input"),
			want: true,
		},
		{
			name: "400 with vision mention",
			err:  errors.New("error 400: this model does not have vision capabilities"),
			want: true,
		},
		{
			name: "400 with unsupported content type",
			err:  errors.New("400: unsupported content type"),
			want: true,
		},
		{
			name: "generic 400 no image keywords",
			err:  errors.New("400 Bad Request: invalid model id"),
			want: false,
		},
		{
			name: "500 server error",
			err:  errors.New("500 Internal Server Error"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "context limit error",
			err:  errors.New("context length exceeded"),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isImageRejectionError(tt.err); got != tt.want {
				t.Errorf("isImageRejectionError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
