package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeAPI is an in-memory SpinupWP API covering the endpoints the provider uses.
// Events are reported as deployed right away, unless failNextEvent is set.
type fakeAPI struct {
	*httptest.Server

	mu            sync.Mutex
	servers       map[int64]map[string]any
	sites         map[int64]map[string]any
	lastCreate    map[string]string // form of the last POST /sites
	lastServer    map[string]string // form of the last POST /servers/custom
	lastDelete    map[string]string // query of the last DELETE /sites/{id}
	lastHTTPS     map[string]string // form of the last HTTPS request
	httpsCalls    int
	lastBasicAuth map[string]string // form of the last basic auth request
	failNextEvent bool
	failedEvents  map[int64]bool
	nextID        int64
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{
		servers: map[int64]map[string]any{
			1: {"id": 1, "name": "my-server", "provider_name": "DigitalOcean", "ip_address": "192.0.2.10",
				"ubuntu_version": "26.04", "status": "provisioned", "database": map[string]any{"server": "external", "host": "db.example.com"}},
			2: {"id": 2, "name": "twin", "status": "provisioned", "database": map[string]any{}},
			3: {"id": 3, "name": "twin", "status": "provisioned", "database": map[string]any{}},
		},
		sites:        map[int64]map[string]any{},
		failedEvents: map[int64]bool{},
		nextID:       100,
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.Close)
	return f
}

// addSite creates a site directly, as if it was made in the SpinupWP dashboard.
func (f *fakeAPI) addSite(serverID int64, domain, user string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	site := newSite(f.nextID, serverID, domain, user)
	site["is_wordpress"] = true
	site["page_cache"] = map[string]any{"enabled": true}
	site["https"] = httpsOn()
	f.sites[f.nextID] = site
	return f.nextID
}

// newSite returns a site as SpinupWP reports it right after creation.
func newSite(id, serverID int64, domain, user string) map[string]any {
	return map[string]any{
		"id": id, "server_id": serverID, "domain": domain, "site_user": user, "php_version": "8.3", "public_folder": "/",
		"is_wordpress": false, "status": "deployed", "page_cache": map[string]any{"enabled": false},
		"cron":       map[string]any{"enabled": true, "interval": 1},
		"https":      map[string]any{"enabled": false},
		"basic_auth": map[string]any{"enabled": false, "username": nil},
		"database":   map[string]any{"id": id + 1000, "table_prefix": "wp_"},
		"backups": map[string]any{
			"database": false, "paths_to_exclude": "node_modules\n/files/vendor", "storage_provider": nil,
			"schedule": map[string]any{
				"daily":   map[string]any{"time_of_day": []int64{}, "retention_period": nil},
				"weekly":  map[string]any{"day_of_week": []int64{}, "time_of_day": nil, "retention_period": nil},
				"monthly": map[string]any{"day_of_month": []any{}, "time_of_day": nil, "retention_period": nil},
			},
		},
	}
}

func httpsOn() map[string]any {
	return map[string]any{"enabled": true, "certificate_expires": "2027-01-31 00:00:00", "certificate_renews": "2027-01-01 00:00:00"}
}

// withOnlySite runs fn on the only site, under the lock, to inspect or change it.
func (f *fakeAPI) withOnlySite(fn func(site map[string]any)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, site := range f.sites {
		fn(site)
		return
	}
}

// deleteAllSites removes every site, as if they were deleted in the SpinupWP dashboard.
func (f *fakeAPI) deleteAllSites() {
	f.mu.Lock()
	defer f.mu.Unlock()
	clear(f.sites)
}

func (f *fakeAPI) siteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sites)
}

func (f *fakeAPI) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer test-token" {
		reply(w, http.StatusUnauthorized, map[string]any{"message": "Unauthenticated."})
		return
	}
	_ = r.ParseForm()
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	id := int64(0)
	if len(parts) > 1 {
		id, _ = strconv.ParseInt(parts[1], 10, 64)
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/servers":
		list := []any{}
		for _, k := range []int64{1, 2, 3} {
			list = append(list, f.servers[k])
		}
		reply(w, 200, map[string]any{"data": list, "pagination": map[string]any{"next": nil}})
	case r.Method == http.MethodPost && r.URL.Path == "/servers/custom":
		f.createServer(w, r)
	case r.Method == http.MethodGet && parts[0] == "servers":
		f.replyFound(w, f.servers[id])
	case r.Method == http.MethodDelete && parts[0] == "servers" && len(parts) == 2:
		if f.servers[id] == nil {
			reply(w, 404, map[string]any{"message": "No query results"})
			return
		}
		delete(f.servers, id)
		reply(w, 200, f.event())
	case r.Method == http.MethodPost && r.URL.Path == "/sites":
		f.createSite(w, r)
	case r.Method == http.MethodGet && parts[0] == "sites" && len(parts) == 2:
		f.replyFound(w, f.sites[id])
	case r.Method == http.MethodDelete && parts[0] == "sites" && len(parts) == 2:
		if f.sites[id] == nil {
			reply(w, 404, map[string]any{"message": "No query results"})
			return
		}
		f.lastDelete = flatten(r.URL.Query())
		for _, k := range []string{"delete_database", "delete_backups"} {
			if v := f.lastDelete[k]; v != "1" && v != "0" {
				reply(w, 422, map[string]any{"message": "The " + k + " field must be true or false."})
				return
			}
		}
		delete(f.sites, id)
		reply(w, 200, f.event())
	case r.Method == http.MethodPut && len(parts) == 3 && parts[2] == "php":
		f.sites[id]["php_version"] = r.PostForm.Get("php_version")
		reply(w, 200, f.event())
	case len(parts) == 3 && parts[2] == "page-cache":
		f.sites[id]["page_cache"] = map[string]any{"enabled": r.Method == http.MethodPost}
		reply(w, 200, f.event())
	case len(parts) == 3 && parts[2] == "cron":
		interval, _ := strconv.Atoi(r.PostForm.Get("interval"))
		enabled := f.sites[id]["cron"].(map[string]any)["enabled"] == true
		if (r.Method == http.MethodPost && enabled) || (r.Method != http.MethodPost && !enabled) {
			reply(w, 422, map[string]any{"message": "wrong cron method for the current state"})
			return
		}
		f.sites[id]["cron"] = map[string]any{"enabled": r.Method != http.MethodDelete, "interval": interval}
		reply(w, 200, f.event())
	case len(parts) == 3 && parts[2] == "https":
		f.httpsCalls++
		f.lastHTTPS = flatten(r.PostForm)
		if r.Method == http.MethodDelete {
			f.sites[id]["https"] = map[string]any{"enabled": false}
		} else {
			f.sites[id]["https"] = httpsOn()
		}
		reply(w, 200, f.event())
	case len(parts) == 3 && parts[2] == "basic-auth":
		f.lastBasicAuth = flatten(r.PostForm)
		if r.Method == http.MethodDelete {
			f.sites[id]["basic_auth"] = map[string]any{"enabled": false, "username": nil}
		} else {
			f.sites[id]["basic_auth"] = map[string]any{"enabled": true, "username": r.PostForm.Get("username")}
		}
		reply(w, 200, f.event())
	case r.Method == http.MethodPut && len(parts) == 3 && parts[2] == "backup-settings":
		f.backupSettings(w, r, f.sites[id])
	case r.Method == http.MethodPut && len(parts) == 3 && parts[2] == "backup-schedule":
		f.backupSchedule(w, r, f.sites[id])
	case r.Method == http.MethodGet && parts[0] == "events":
		status := "deployed"
		if f.failedEvents[id] {
			status = "failed"
		}
		reply(w, 200, map[string]any{"data": map[string]any{"id": id, "name": "test event", "status": status, "output": "deploy script exited 1"}})
	default:
		reply(w, 404, map[string]any{"message": "not found: " + r.Method + " " + r.URL.Path})
	}
}

func (f *fakeAPI) createSite(w http.ResponseWriter, r *http.Request) {
	form := flatten(r.PostForm)
	// Like the real API, form booleans must be 1 or 0.
	if v, ok := form["page_cache[enabled]"]; ok && v != "1" && v != "0" {
		reply(w, 422, map[string]any{"message": "The is cache enabled field must be true or false."})
		return
	}
	for _, s := range f.sites {
		if s["domain"] == form["domain"] {
			reply(w, 422, map[string]any{"message": "The given data was invalid.",
				"errors": map[string][]string{"domain": {"The domain has already been taken."}}})
			return
		}
	}
	php := form["php_version"]
	if php == "" {
		php = "8.3"
	}
	serverID, _ := strconv.ParseInt(form["server_id"], 10, 64)
	f.nextID++
	site := newSite(f.nextID, serverID, form["domain"], form["site_user"])
	site["php_version"] = php
	if form["public_folder"] != "" {
		site["public_folder"] = strings.TrimSuffix(form["public_folder"], "/") + "/"
	}
	site["is_wordpress"] = strings.HasPrefix(form["installation_method"], "wp")
	site["page_cache"] = map[string]any{"enabled": form["page_cache[enabled]"] == "true"}
	f.sites[f.nextID] = site
	f.lastCreate = form
	reply(w, 200, map[string]any{"event_id": f.event()["event_id"], "data": site})
}

// createServer handles POST /servers/custom. The server is provisioned once its event is.
func (f *fakeAPI) createServer(w http.ResponseWriter, r *http.Request) {
	form := flatten(r.PostForm)
	for _, k := range []string{"provider_name", "ubuntu_version", "ip_address", "username", "hostname"} {
		if form[k] == "" {
			reply(w, 422, map[string]any{"message": "The given data was invalid.",
				"errors": map[string][]string{k: {"The " + k + " field is required."}}})
			return
		}
	}
	f.nextID++
	server := map[string]any{"id": f.nextID, "name": form["hostname"], "provider_name": form["provider_name"],
		"ip_address": form["ip_address"], "ubuntu_version": form["ubuntu_version"], "status": "provisioned",
		"database": map[string]any{"server": "mysql-8.4", "host": "localhost"}}
	f.servers[f.nextID] = server
	f.lastServer = form
	reply(w, 200, map[string]any{"event_id": f.event()["event_id"], "data": server})
}

func (f *fakeAPI) serverExists(id int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.servers[id] != nil
}

// backupSettings handles PUT /sites/{id}/backup-settings (a JSON body).
func (f *fakeAPI) backupSettings(w http.ResponseWriter, r *http.Request, site map[string]any) {
	var body struct {
		StorageProviderID int64   `json:"storage_provider_id"`
		Bucket            string  `json:"storage_provider_bucket"`
		Region            string  `json:"storage_provider_region"`
		Database          *int64  `json:"database"`
		PathsToExclude    *string `json:"paths_to_exclude"`
	}
	if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&body) != nil {
		reply(w, 400, map[string]any{"message": "expected a JSON body"})
		return
	}
	b := site["backups"].(map[string]any)
	b["storage_provider"] = map[string]any{"id": body.StorageProviderID, "bucket": body.Bucket, "region": body.Region}
	b["database"] = body.Database != nil
	if body.PathsToExclude != nil {
		b["paths_to_exclude"] = *body.PathsToExclude
	}
	reply(w, 200, map[string]any{"data": site})
}

// backupSchedule handles PUT /sites/{id}/backup-schedule. An empty list of times or days disables a schedule.
func (f *fakeAPI) backupSchedule(w http.ResponseWriter, r *http.Request, site map[string]any) {
	var body map[string]map[string]any
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		reply(w, 400, map[string]any{"message": "expected a JSON body"})
		return
	}
	schedule := site["backups"].(map[string]any)["schedule"].(map[string]any)
	for in, out := range map[string]string{"daily_schedule": "daily", "weekly_schedule": "weekly", "monthly_schedule": "monthly"} {
		if s, ok := body[in]; ok {
			schedule[out] = s
		}
	}
	reply(w, 200, map[string]any{"data": site})
}

func (f *fakeAPI) event() map[string]any {
	f.nextID++
	if f.failNextEvent {
		f.failedEvents[f.nextID] = true
		f.failNextEvent = false
	}
	return map[string]any{"event_id": f.nextID}
}

func (f *fakeAPI) replyFound(w http.ResponseWriter, v map[string]any) {
	if v == nil {
		reply(w, 404, map[string]any{"message": "No query results"})
		return
	}
	reply(w, 200, map[string]any{"data": v})
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func flatten(v map[string][]string) map[string]string {
	out := map[string]string{}
	for k, vals := range v {
		out[k] = strings.Join(vals, ",")
	}
	return out
}

// providerConfig points the provider at the fake API.
func (f *fakeAPI) providerConfig() string {
	return fmt.Sprintf(`
provider "spinupwp" {
  api_token = "test-token"
  api_url   = %q
}
`, f.URL)
}
