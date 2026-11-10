package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Server is a SpinupWP server. Only the fields the provider uses are decoded.
type Server struct {
	ID            int64          `json:"id"`
	Name          string         `json:"name"`
	ProviderName  string         `json:"provider_name"`
	IPAddress     string         `json:"ip_address"`
	UbuntuVersion string         `json:"ubuntu_version"`
	Status        string         `json:"status"`
	Database      ServerDatabase `json:"database"`
}

// ServerDatabase is the database server a SpinupWP server uses.
// The API's port is left out: it is documented as a number but returned as a string.
type ServerDatabase struct {
	Server string `json:"server"`
	Host   string `json:"host"`
}

// GetServer returns a server by ID.
func (c *Client) GetServer(ctx context.Context, id int64) (*Server, error) {
	var out struct {
		Data Server `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/servers/%d", id), nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// ListServers returns all servers, following pagination.
func (c *Client) ListServers(ctx context.Context) ([]Server, error) {
	var servers []Server
	for page := 1; ; page++ {
		var out struct {
			Data       []Server `json:"data"`
			Pagination struct {
				Next *string `json:"next"`
			} `json:"pagination"`
		}
		q := url.Values{"limit": {"100"}, "page": {strconv.Itoa(page)}}
		if err := c.do(ctx, http.MethodGet, "/servers?"+q.Encode(), nil, &out); err != nil {
			return nil, err
		}
		servers = append(servers, out.Data...)
		if out.Pagination.Next == nil || *out.Pagination.Next == "" || len(out.Data) == 0 {
			return servers, nil
		}
	}
}

// CreateCustomServerRequest holds the parameters for connecting a server from any provider.
type CreateCustomServerRequest struct {
	ProviderName         string
	UbuntuVersion        string
	IPAddress            string
	Username             string
	Hostname             string
	SSHPort              int64
	Timezone             string
	PostProvisionScript  string
	DatabaseRootPassword string
}

func (r CreateCustomServerRequest) form() url.Values {
	f := url.Values{}
	f.Set("provider_name", r.ProviderName)
	f.Set("ubuntu_version", r.UbuntuVersion)
	f.Set("ip_address", r.IPAddress)
	f.Set("username", r.Username)
	f.Set("hostname", r.Hostname)
	// SpinupWP connects with its own SSH key, which must already be in the user's authorized_keys
	f.Set("auth_method", "publickey")
	if r.SSHPort != 0 {
		f.Set("ssh_port", strconv.FormatInt(r.SSHPort, 10))
	}
	setIfNotEmpty(f, "timezone", r.Timezone)
	setIfNotEmpty(f, "post_provision_script", r.PostProvisionScript)
	setIfNotEmpty(f, "database_root_password", r.DatabaseRootPassword)
	return f
}

// CreateCustomServer starts provisioning a custom server. It returns the server and the event to wait for.
func (c *Client) CreateCustomServer(ctx context.Context, r CreateCustomServerRequest) (*Server, int64, error) {
	var out struct {
		EventID int64  `json:"event_id"`
		Data    Server `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/servers/custom", r.form(), &out); err != nil {
		return nil, 0, err
	}
	return &out.Data, out.EventID, nil
}

// DeleteServer removes a server from SpinupWP and returns the event to wait for (0 if none).
// The machine itself is left alone.
func (c *Client) DeleteServer(ctx context.Context, id int64) (int64, error) {
	var out eventResponse
	err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil, &out)
	return out.EventID, err
}

// GetSSHKey returns the public key SpinupWP connects with while provisioning a custom server.
func (c *Client) GetSSHKey(ctx context.Context) (string, error) {
	var out struct {
		Key string `json:"key"`
	}
	if err := c.do(ctx, http.MethodGet, "/ssh-key", nil, &out); err != nil {
		return "", err
	}
	return out.Key, nil
}
