package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// HTTPS is a site's HTTPS status.
type HTTPS struct {
	Enabled            bool   `json:"enabled"`
	CertificateExpires string `json:"certificate_expires"`
	CertificateRenews  string `json:"certificate_renews"`
}

// HTTPSRequest enables or updates HTTPS. Type is "webroot", "dns" or "custom".
type HTTPSRequest struct {
	Type          string
	DNSProviderID *int64
	Certificate   string
	PrivateKey    string
}

func (r HTTPSRequest) form() url.Values {
	f := url.Values{"type": {r.Type}}
	if r.DNSProviderID != nil {
		f.Set("dns_provider_id", strconv.FormatInt(*r.DNSProviderID, 10))
	}
	setIfNotEmpty(f, "certificate", r.Certificate)
	setIfNotEmpty(f, "private_key", r.PrivateKey)
	return f
}

// EnableHTTPS enables HTTPS on a site and returns the event to wait for.
func (c *Client) EnableHTTPS(ctx context.Context, siteID int64, r HTTPSRequest) (int64, error) {
	return c.siteEvent(ctx, http.MethodPost, siteID, "https", r.form())
}

// UpdateHTTPS changes a site's certificate type or custom certificate.
func (c *Client) UpdateHTTPS(ctx context.Context, siteID int64, r HTTPSRequest) (int64, error) {
	return c.siteEvent(ctx, http.MethodPut, siteID, "https", r.form())
}

// DisableHTTPS disables HTTPS on a site.
func (c *Client) DisableHTTPS(ctx context.Context, siteID int64) (int64, error) {
	return c.siteEvent(ctx, http.MethodDelete, siteID, "https", nil)
}

// BasicAuth is a site's HTTP basic authentication status. The password is never returned.
type BasicAuth struct {
	Enabled  bool   `json:"enabled"`
	Username string `json:"username"`
}

// EnableBasicAuth turns on basic auth for a site.
func (c *Client) EnableBasicAuth(ctx context.Context, siteID int64, username, password string) (int64, error) {
	return c.siteEvent(ctx, http.MethodPost, siteID, "basic-auth", url.Values{"username": {username}, "password": {password}})
}

// UpdateBasicAuth changes the basic auth credentials. An empty password keeps the current one.
func (c *Client) UpdateBasicAuth(ctx context.Context, siteID int64, username, password string) (int64, error) {
	f := url.Values{"username": {username}}
	setIfNotEmpty(f, "password", password)
	return c.siteEvent(ctx, http.MethodPut, siteID, "basic-auth", f)
}

// DisableBasicAuth turns off basic auth for a site.
func (c *Client) DisableBasicAuth(ctx context.Context, siteID int64) (int64, error) {
	return c.siteEvent(ctx, http.MethodDelete, siteID, "basic-auth", nil)
}

// Cron is a site's server-level WordPress cron.
type Cron struct {
	Enabled  bool   `json:"enabled"`
	Interval *int64 `json:"interval"`
}

// Minutes returns the interval in minutes, or 0 when cron is off.
func (c Cron) Minutes() int64 {
	if !c.Enabled || c.Interval == nil {
		return 0
	}
	return *c.Interval
}

// EnableCron turns on the server-level cron at an interval in minutes.
func (c *Client) EnableCron(ctx context.Context, siteID, minutes int64) (int64, error) {
	return c.siteEvent(ctx, http.MethodPost, siteID, "cron", url.Values{"interval": {strconv.FormatInt(minutes, 10)}})
}

// UpdateCron changes the cron interval.
func (c *Client) UpdateCron(ctx context.Context, siteID, minutes int64) (int64, error) {
	return c.siteEvent(ctx, http.MethodPut, siteID, "cron", url.Values{"interval": {strconv.FormatInt(minutes, 10)}})
}

// DisableCron turns off the server-level cron.
func (c *Client) DisableCron(ctx context.Context, siteID int64) (int64, error) {
	return c.siteEvent(ctx, http.MethodDelete, siteID, "cron", nil)
}

// siteEvent calls a site sub-endpoint that answers with an event ID.
// The ID is 0 when SpinupWP had nothing to do.
func (c *Client) siteEvent(ctx context.Context, method string, siteID int64, endpoint string, form url.Values) (int64, error) {
	var out struct {
		EventID *int64 `json:"event_id"`
	}
	var body any
	if form != nil {
		body = form
	}
	if err := c.do(ctx, method, fmt.Sprintf("/sites/%d/%s", siteID, endpoint), body, &out); err != nil {
		return 0, err
	}
	if out.EventID == nil {
		return 0, nil
	}
	return *out.EventID, nil
}

// Backups is a site's backup configuration.
type Backups struct {
	Database        bool           `json:"database"`
	PathsToExclude  string         `json:"paths_to_exclude"`
	StorageProvider *BackupStorage `json:"storage_provider"`
	Schedule        BackupSchedule `json:"schedule"`
}

// BackupStorage is where a site's backups are stored.
type BackupStorage struct {
	ID     int64  `json:"id"`
	Region string `json:"region"`
	Bucket string `json:"bucket"`
}

// BackupSchedule holds the daily, weekly and monthly backup schedules.
type BackupSchedule struct {
	Daily   DailyBackups   `json:"daily"`
	Weekly  WeeklyBackups  `json:"weekly"`
	Monthly MonthlyBackups `json:"monthly"`
}

// BackupContent selects what a schedule backs up and for how long it is kept.
type BackupContent struct {
	BackupDatabase  bool   `json:"backup_database"`
	BackupFiles     bool   `json:"backup_files"`
	RetentionPeriod *int64 `json:"retention_period"`
}

// DailyBackups run at the given hours (0-23). No hours means disabled.
type DailyBackups struct {
	BackupContent
	TimeOfDay []int64 `json:"time_of_day"`
}

// WeeklyBackups run on the given weekdays (0 = Sunday) at one hour. No days means disabled.
type WeeklyBackups struct {
	BackupContent
	TimeOfDay *int64  `json:"time_of_day"`
	DayOfWeek []int64 `json:"day_of_week"`
}

// MonthlyBackups run on days like "first Monday" at one hour. No days means disabled.
type MonthlyBackups struct {
	BackupContent
	TimeOfDay  *int64     `json:"time_of_day"`
	DayOfMonth []MonthDay `json:"day_of_month"`
}

// MonthDay is a day of the month such as the first Monday.
type MonthDay struct {
	WeekOfMonth string `json:"week_of_month"` // first, second, third or last
	DayOfWeek   int64  `json:"day_of_week"`   // 0 = Sunday
}

// BackupSettingsRequest sets where backups go and what they include.
type BackupSettingsRequest struct {
	StorageProviderID int64
	Bucket            string
	Region            string
	StorageClass      string
	// DatabaseID is the database to back up; nil stops backing up a database.
	DatabaseID     *int64
	PathsToExclude string
}

// UpdateBackupSettings sets a site's backup storage and contents.
func (c *Client) UpdateBackupSettings(ctx context.Context, siteID int64, r BackupSettingsRequest) (*Site, error) {
	body := map[string]any{
		"storage_provider_id":     r.StorageProviderID,
		"storage_provider_bucket": r.Bucket,
		"database":                r.DatabaseID, // null clears it
		"paths_to_exclude":        r.PathsToExclude,
	}
	if r.Region != "" {
		body["storage_provider_region"] = r.Region
	}
	if r.StorageClass != "" {
		body["storage_provider_class"] = r.StorageClass
	}
	return c.siteUpdate(ctx, siteID, "backup-settings", body)
}

// BackupScheduleRequest replaces a site's backup schedules. A nil schedule is disabled.
type BackupScheduleRequest struct {
	Daily   *DailyBackups
	Weekly  *WeeklyBackups
	Monthly *MonthlyBackups
}

// UpdateBackupSchedule sets a site's backup schedules.
func (c *Client) UpdateBackupSchedule(ctx context.Context, siteID int64, r BackupScheduleRequest) (*Site, error) {
	// The API disables a schedule when it gets an empty list of times or days.
	body := map[string]any{
		"daily_schedule":   map[string]any{"time_of_day": []int64{}},
		"weekly_schedule":  map[string]any{"day_of_week": []int64{}},
		"monthly_schedule": map[string]any{"day_of_month": []MonthDay{}},
	}
	if d := r.Daily; d != nil {
		body["daily_schedule"] = schedule(d.BackupContent, "time_of_day", d.TimeOfDay, nil)
	}
	if w := r.Weekly; w != nil {
		body["weekly_schedule"] = schedule(w.BackupContent, "day_of_week", w.DayOfWeek, w.TimeOfDay)
	}
	if m := r.Monthly; m != nil {
		body["monthly_schedule"] = schedule(m.BackupContent, "day_of_month", m.DayOfMonth, m.TimeOfDay)
	}
	return c.siteUpdate(ctx, siteID, "backup-schedule", body)
}

func schedule(content BackupContent, whenKey string, when any, hour *int64) map[string]any {
	s := map[string]any{
		whenKey:            when,
		"backup_database":  content.BackupDatabase,
		"backup_files":     content.BackupFiles,
		"retention_period": content.RetentionPeriod,
	}
	if hour != nil {
		s["time_of_day"] = *hour
	}
	return s
}

// siteUpdate calls a site sub-endpoint that answers with the updated site.
func (c *Client) siteUpdate(ctx context.Context, siteID int64, endpoint string, body map[string]any) (*Site, error) {
	var out struct {
		Data Site `json:"data"`
	}
	if err := c.do(ctx, http.MethodPut, fmt.Sprintf("/sites/%d/%s", siteID, endpoint), body, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}
