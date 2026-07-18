package httpx

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDoJSONRetriesThrottleAndServerErrors(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		attempts++
		if request.Method != http.MethodPost {
			t.Errorf("method = %s", request.Method)
		}
		if attempts < 3 {
			response.Header().Set("Retry-After", "0")
			response.WriteHeader(map[bool]int{true: http.StatusTooManyRequests, false: http.StatusBadGateway}[attempts == 1])
			_, _ = fmt.Fprint(response, "retry")
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(response, `{"ok":true}`)
	}))
	defer server.Close()

	request, err := http.NewRequest(http.MethodPost, server.URL, bytes.NewReader([]byte(`{"query":"value"}`)))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if err := DoJSON(context.Background(), server.Client(), request, &result); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 || !result.OK {
		t.Fatalf("attempts=%d result=%+v", attempts, result)
	}
}

func TestDoJSONDoesNotRetryClientErrors(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		attempts++
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := DoJSON(context.Background(), server.Client(), request, nil); err == nil {
		t.Fatal("forbidden response was accepted")
	}
	if attempts != 1 {
		t.Fatalf("client error was retried %d times", attempts)
	}
}
