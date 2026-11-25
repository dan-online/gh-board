package gh

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A write that hits a secondary rate limit is resent by the limiter.
// The resent request must carry the same body, not the drained one.
func TestRetryAfterSecondaryLimitResendsBody(t *testing.T) {
	var calls atomic.Int32
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"message":"You have exceeded a secondary rate limit"}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := &http.Client{Transport: transport(), Timeout: 10 * time.Second}
	resp, err := client.Post(srv.URL, "application/json", bytes.NewReader([]byte(`{"title":"x"}`)))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || len(bodies) != 2 {
		t.Fatalf("status %d after %d calls", resp.StatusCode, len(bodies))
	}
	if bodies[1] != `{"title":"x"}` {
		t.Fatalf("retried body was %q", bodies[1])
	}
}
