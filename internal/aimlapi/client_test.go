package aimlapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateKeySendsAuthenticatedRequestAndDecodesKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/keys" {
			t.Fatalf("request = %s %s, want POST /v1/keys", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer session-token" {
			t.Fatalf("Authorization = %q", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["name"] != "Zero CLI" {
			t.Fatalf("name = %q", body["name"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"sk-issued","id":"key-id"}`))
	}))
	defer server.Close()

	client := NewClient(Endpoints{AppBaseURL: server.URL}, server.Client())
	key, err := client.CreateKey(context.Background(), " session-token ", " Zero CLI ")
	if err != nil {
		t.Fatal(err)
	}
	if key.Key != "sk-issued" || key.ID != "key-id" {
		t.Fatalf("key = %#v", key)
	}
}

func TestCreateKeyRejectsEmptyKeyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"key-id"}`))
	}))
	defer server.Close()

	client := NewClient(Endpoints{AppBaseURL: server.URL}, server.Client())
	if _, err := client.CreateKey(context.Background(), "session-token", "Zero"); err == nil {
		t.Fatal("CreateKey accepted a response without an API key")
	}
}

func TestAuthMethodsRejectEmptyTokenResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":""}`))
	}))
	defer server.Close()

	client := NewClient(Endpoints{AuthBaseURL: server.URL}, server.Client())
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "verify sign-in code",
			call: func() error {
				_, err := client.VerifySignInCode(context.Background(), "user@example.com", "123456")
				return err
			},
		},
		{
			name: "create passwordless account",
			call: func() error {
				_, err := client.CreatePasswordlessAccount(context.Background(), "user@example.com")
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); err == nil {
				t.Fatal("accepted a response without an auth token")
			}
		})
	}
}

func TestClientReturnsTypedAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "invalid key", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewClient(Endpoints{InferenceBaseURL: server.URL}, server.Client())
	_, err := client.GetBalance(context.Background(), "bad-key")
	var apiErr APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v, want APIError", err, err)
	}
	if apiErr.Status != http.StatusUnauthorized || apiErr.Body != "invalid key\n" {
		t.Fatalf("APIError = %#v", apiErr)
	}
}

func TestTypedResponseRejectsEmptySuccessBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient(Endpoints{InferenceBaseURL: server.URL}, server.Client())
	if _, err := client.GetBalance(context.Background(), "key"); err == nil {
		t.Fatal("GetBalance accepted an empty successful response")
	}
}

func TestBodylessOperationAcceptsEmptySuccessBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient(Endpoints{AuthBaseURL: server.URL}, server.Client())
	if err := client.SendSignInCode(context.Background(), "user@example.com"); err != nil {
		t.Fatalf("SendSignInCode() error = %v", err)
	}
}

func TestParseAmountUSD(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{name: "default", want: DefaultAmountUSDMinor},
		{name: "minimum", value: "20", want: 2000},
		{name: "cents", value: "25.49", want: 2549},
		{name: "below minimum", value: "19.99", wantErr: true},
		{name: "above maximum", value: "10001", wantErr: true},
		{name: "not a number", value: "twenty", wantErr: true},
		{name: "nan", value: "NaN", wantErr: true},
		{name: "infinity", value: "Inf", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseAmountUSD(test.value)
			if (err != nil) != test.wantErr {
				t.Fatalf("ParseAmountUSD(%q) error = %v", test.value, err)
			}
			if got != test.want {
				t.Fatalf("ParseAmountUSD(%q) = %d, want %d", test.value, got, test.want)
			}
		})
	}
}
