package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New("test-token", WithBaseURL(srv.URL), WithRetry(2, time.Millisecond))
	c.PollInterval = time.Millisecond
	return c
}

func TestCreateSiteSendsFormAndAuth(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/sites" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{
			"server_id":                 "7",
			"domain":                    "example.com",
			"site_user":                 "example",
			"installation_method":       "wp",
			"page_cache[enabled]":       "1",
			"database[name]":            "example",
			"wordpress[admin_password]": "s3cret-pass",
			"wordpress[admin_email]":    "admin@example.com",
		}
		for k, v := range want {
			if got := r.PostForm.Get(k); got != v {
				t.Errorf("form %s = %q, want %q", k, got, v)
			}
		}
		if r.PostForm.Has("php_version") {
			t.Error("empty php_version should not be sent")
		}
		_, _ = w.Write([]byte(`{"event_id": 42, "data": {"id": 99, "server_id": 7, "domain": "example.com"}}`))
	})

	enabled := true
	site, eventID, err := c.CreateSite(context.Background(), CreateSiteRequest{
		ServerID:           7,
		Domain:             "example.com",
		SiteUser:           "example",
		InstallationMethod: "wp",
		PageCacheEnabled:   &enabled,
		Database:           &CreateSiteDatabase{Name: "example"},
		WordPress:          &CreateSiteWordPress{Title: "Example", AdminUser: "admin", AdminEmail: "admin@example.com", AdminPassword: "s3cret-pass"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if site.ID != 99 || eventID != 42 {
		t.Errorf("site.ID = %d, eventID = %d", site.ID, eventID)
	}
}

func TestErrorDecoding(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message": "The given data was invalid.", "errors": {"domain": ["The domain has already been taken."]}}`))
	})
	_, _, err := c.CreateSite(context.Background(), CreateSiteRequest{})
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 422 {
		t.Fatalf("err = %v, want 422 *Error", err)
	}
	if !strings.Contains(err.Error(), "domain has already been taken") {
		t.Errorf("error message missing field detail: %v", err)
	}
}

func TestIsNotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message": "No query results"}`))
	})
	_, err := c.GetSite(context.Background(), 1)
	if !IsNotFound(err) {
		t.Fatalf("IsNotFound(%v) = false", err)
	}
}

func TestRetriesRateLimitThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"data": {"id": 5}}`))
	})
	site, err := c.GetSite(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if site.ID != 5 || calls.Load() != 2 {
		t.Errorf("site.ID = %d after %d calls", site.ID, calls.Load())
	}
}

func TestDoesNotRetryServerErrorOnWrite(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	if _, err := c.UpdatePHPVersion(context.Background(), 1, "8.3"); err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Errorf("PUT was sent %d times, want 1", calls.Load())
	}
}

func TestWaitForEvent(t *testing.T) {
	tests := map[string]struct {
		statuses []string
		wantErr  bool
	}{
		"deployed after queued": {statuses: []string{"queued", "creating", "deployed"}},
		"failed":                {statuses: []string{"queued", "failed"}, wantErr: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				status := tt.statuses[min(int(calls.Add(1))-1, len(tt.statuses)-1)]
				_, _ = w.Write([]byte(`{"data": {"id": 3, "name": "Creating site", "status": "` + status + `", "output": "boom"}}`))
			})
			err := c.WaitForEvent(context.Background(), 3)
			var failed *EventFailedError
			if tt.wantErr != errors.As(err, &failed) {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), "boom") {
				t.Errorf("failure output missing: %v", err)
			}
		})
	}
}

func TestWaitForSiteDeployed(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		status := "deploying"
		if calls.Add(1) >= 3 {
			status = "deployed"
		}
		_, _ = w.Write([]byte(`{"data": {"id": 9, "domain": "example.com", "status": "` + status + `"}}`))
	})
	site, err := c.WaitForSiteDeployed(context.Background(), 9)
	if err != nil || site.Status != "deployed" || calls.Load() != 3 {
		t.Fatalf("site = %+v, err = %v after %d calls", site, err, calls.Load())
	}
}

func TestWaitForSiteDeployedFails(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data": {"id": 9, "domain": "example.com", "status": "failed"}}`))
	})
	if _, err := c.WaitForSiteDeployed(context.Background(), 9); err == nil || !strings.Contains(err.Error(), `"failed"`) {
		t.Fatalf("err = %v, want a failed status error", err)
	}
}

func TestWaitForEventHonoursContext(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data": {"id": 3, "status": "queued"}}`))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.WaitForEvent(ctx, 3); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

// The real API returns database.port as a string, unlike its docs.
func TestGetServerDecodesRealResponse(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data": {"id": 7, "name": "my-server", "ubuntu_version": "26.04",
			"database": {"server": "external", "host": "db.example.com", "port": "25060"}}}`))
	})
	s, err := c.GetServer(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "my-server" || s.Database.Host != "db.example.com" {
		t.Errorf("server = %+v", s)
	}
}

func TestListServersFollowsPagination(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(`{"data": [{"id": 1, "name": "a"}], "pagination": {"next": "page2"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data": [{"id": 2, "name": "b"}], "pagination": {"next": null}}`))
	})
	servers, err := c.ListServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 || servers[1].Name != "b" {
		t.Errorf("servers = %+v", servers)
	}
}
