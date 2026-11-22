package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// SiteDomain is an additional domain of a site.
type SiteDomain struct {
	ID       int64          `json:"id"`
	Domain   string         `json:"domain"`
	Redirect DomainRedirect `json:"redirect"`
}

// DomainRedirect sends an additional domain's requests elsewhere. Type is 301, 302, 307 or 308;
// SpinupWP defaults it to 301 and Destination to the site's primary domain.
type DomainRedirect struct {
	Enabled     bool   `json:"enabled"`
	Type        int64  `json:"type"`
	Destination string `json:"destination"`
}

func (r DomainRedirect) setForm(f url.Values) {
	f.Set("redirect[enabled]", formBool(r.Enabled))
	if !r.Enabled {
		return
	}
	if r.Type != 0 {
		f.Set("redirect[type]", strconv.FormatInt(r.Type, 10))
	}
	setIfNotEmpty(f, "redirect[destination]", r.Destination)
}

// AddSiteDomain starts adding an additional domain to a site and returns the event to wait for.
// The site's additional_domains has it once the event is done.
func (c *Client) AddSiteDomain(ctx context.Context, siteID int64, domain string, redirect DomainRedirect) (int64, error) {
	f := url.Values{"domain": {domain}}
	redirect.setForm(f)
	return c.siteEvent(ctx, http.MethodPost, siteID, "domains", f)
}

// UpdateSiteDomain changes an additional domain's redirect and returns the event to wait for.
func (c *Client) UpdateSiteDomain(ctx context.Context, siteID, domainID int64, redirect DomainRedirect) (int64, error) {
	f := url.Values{}
	redirect.setForm(f)
	return c.siteEvent(ctx, http.MethodPut, siteID, fmt.Sprintf("domains/%d", domainID), f)
}

// DeleteSiteDomain starts removing an additional domain from a site and returns the event to wait for.
func (c *Client) DeleteSiteDomain(ctx context.Context, siteID, domainID int64) (int64, error) {
	return c.siteEvent(ctx, http.MethodDelete, siteID, fmt.Sprintf("domains/%d", domainID), nil)
}
