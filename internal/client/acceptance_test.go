package client

import (
	"context"
	"os"
	"strconv"
	"testing"
)

// TestAccGetSite decodes a real site (read-only). Needs TF_ACC, SPINUPWP_API_TOKEN and SPINUPWP_ACC_SITE_ID.
func TestAccGetSite(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("set TF_ACC to run acceptance tests")
	}
	id, err := strconv.ParseInt(os.Getenv("SPINUPWP_ACC_SITE_ID"), 10, 64)
	if err != nil {
		t.Skip("set SPINUPWP_ACC_SITE_ID to an existing site ID")
	}
	site, err := New(os.Getenv("SPINUPWP_API_TOKEN")).GetSite(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if site.ID != id || site.Domain == "" || site.SiteUser == "" || site.PHPVersion == "" || site.Status == "" {
		t.Errorf("incomplete site: %+v", site)
	}
	t.Logf("decoded %s: php=%s status=%s page_cache=%t https=%t wordpress=%t",
		site.Domain, site.PHPVersion, site.Status, site.PageCache.Enabled, site.HTTPS.Enabled, site.IsWordPress)
}
