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
