package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Site is a SpinupWP site. Only the fields the provider uses are decoded.
type Site struct {
	ID            int64     `json:"id"`
	ServerID      int64     `json:"server_id"`
	Domain        string    `json:"domain"`
	SiteUser      string    `json:"site_user"`
	PHPVersion    string    `json:"php_version"`
	IsWordPress   bool      `json:"is_wordpress"`
	DNSProviderID *int64    `json:"dns_provider_id"`
	Status        string    `json:"status"`
	PageCache     Toggle    `json:"page_cache"`
	Cron          Cron      `json:"cron"`
	HTTPS         HTTPS     `json:"https"`
	BasicAuth     BasicAuth `json:"basic_auth"`
	Backups       Backups   `json:"backups"`
	Database      *Database `json:"database"`
}

// Toggle is a feature that can be enabled or disabled.
type Toggle struct {
	Enabled bool `json:"enabled"`
}

// Database is the database attached to a site.
type Database struct {
	ID          int64  `json:"id"`
	TablePrefix string `json:"table_prefix"`
}

// CreateSiteRequest holds the parameters for creating a site.
type CreateSiteRequest struct {
	ServerID           int64
	Domain             string
	SiteUser           string
	InstallationMethod string
	PHPVersion         string
	PageCacheEnabled   *bool
	DNSProviderID      *int64
	Database           *CreateSiteDatabase
	WordPress          *CreateSiteWordPress
}

// CreateSiteDatabase holds the database to create with a site.
type CreateSiteDatabase struct {
	Name        string
	Username    string
	Password    string
	TablePrefix string
}

// CreateSiteWordPress holds the WordPress install settings.
type CreateSiteWordPress struct {
	Title         string
	AdminUser     string
	AdminEmail    string
	AdminPassword string
}

func (r CreateSiteRequest) form() url.Values {
	f := url.Values{}
	f.Set("server_id", strconv.FormatInt(r.ServerID, 10))
	f.Set("domain", r.Domain)
	f.Set("site_user", r.SiteUser)
	f.Set("installation_method", r.InstallationMethod)
	setIfNotEmpty(f, "php_version", r.PHPVersion)
	if r.PageCacheEnabled != nil {
		f.Set("page_cache[enabled]", formBool(*r.PageCacheEnabled))
	}
	if r.DNSProviderID != nil {
		f.Set("dns_provider_id", strconv.FormatInt(*r.DNSProviderID, 10))
	}
	if db := r.Database; db != nil {
		setIfNotEmpty(f, "database[name]", db.Name)
		setIfNotEmpty(f, "database[username]", db.Username)
		setIfNotEmpty(f, "database[password]", db.Password)
		setIfNotEmpty(f, "database[table_prefix]", db.TablePrefix)
	}
	if wp := r.WordPress; wp != nil {
		f.Set("wordpress[title]", wp.Title)
		f.Set("wordpress[admin_user]", wp.AdminUser)
		f.Set("wordpress[admin_email]", wp.AdminEmail)
		f.Set("wordpress[admin_password]", wp.AdminPassword)
	}
	return f
}

// formBool encodes a boolean the way the API's validation accepts it in form and query
// parameters: "1" or "0" ("true" and "false" are rejected).
func formBool(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func setIfNotEmpty(f url.Values, key, value string) {
	if value != "" {
		f.Set(key, value)
	}
}

type eventResponse struct {
	EventID int64 `json:"event_id"`
}

// CreateSite starts creating a site. It returns the site and the event to wait for.
func (c *Client) CreateSite(ctx context.Context, r CreateSiteRequest) (*Site, int64, error) {
	var out struct {
		EventID int64 `json:"event_id"`
		Data    Site  `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/sites", r.form(), &out); err != nil {
		return nil, 0, err
	}
	return &out.Data, out.EventID, nil
}

// GetSite returns a site by ID.
func (c *Client) GetSite(ctx context.Context, id int64) (*Site, error) {
	var out struct {
		Data Site `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/sites/%d", id), nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// Site statuses. A site can still report "deploying" after its create event is deployed.
const (
	SiteStatusDeployed  = "deployed"
	SiteStatusDeploying = "deploying"
)

// WaitForSiteDeployed polls a site until its status is "deployed".
// Any status other than "deploying" is treated as a failure.
func (c *Client) WaitForSiteDeployed(ctx context.Context, id int64) (*Site, error) {
	for {
		site, err := c.GetSite(ctx, id)
		if err != nil {
			return nil, err
		}
		switch site.Status {
		case SiteStatusDeployed:
			return site, nil
		case SiteStatusDeploying:
		default:
			return site, fmt.Errorf("spinupwp: site %s finished with status %q", site.Domain, site.Status)
		}
		if err := sleep(ctx, c.PollInterval); err != nil {
			return nil, fmt.Errorf("spinupwp: waiting for site %s to finish deploying: %w", site.Domain, err)
		}
	}
}

// DeleteSiteOptions selects what is deleted along with a site.
type DeleteSiteOptions struct {
	DeleteDatabase bool
	DeleteBackups  bool
}

// DeleteSite starts deleting a site and returns the event to wait for.
func (c *Client) DeleteSite(ctx context.Context, id int64, opts DeleteSiteOptions) (int64, error) {
	q := url.Values{}
	q.Set("delete_database", formBool(opts.DeleteDatabase))
	q.Set("delete_backups", formBool(opts.DeleteBackups))
	var out eventResponse
	if err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/sites/%d?%s", id, q.Encode()), nil, &out); err != nil {
		return 0, err
	}
	return out.EventID, nil
}

// UpdatePHPVersion changes a site's PHP version and returns the event to wait for.
func (c *Client) UpdatePHPVersion(ctx context.Context, id int64, version string) (int64, error) {
	var out eventResponse
	err := c.do(ctx, http.MethodPut, fmt.Sprintf("/sites/%d/php", id), url.Values{"php_version": {version}}, &out)
	return out.EventID, err
}

// SetPageCache enables or disables a site's page cache and returns the event to wait for.
func (c *Client) SetPageCache(ctx context.Context, id int64, enabled bool) (int64, error) {
	method := http.MethodPost
	if !enabled {
		method = http.MethodDelete
	}
	var out eventResponse
	err := c.do(ctx, method, fmt.Sprintf("/sites/%d/page-cache", id), nil, &out)
	return out.EventID, err
}
