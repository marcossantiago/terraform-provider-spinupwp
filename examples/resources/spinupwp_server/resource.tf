# SpinupWP's SSH key must be in root's authorized_keys before the server is created
resource "spinupwp_server" "example" {
  hostname       = "my-server"
  ip_address     = "203.0.113.10"
  provider_name  = "Example Hosting"
  ubuntu_version = "26.04"
}

resource "spinupwp_site" "example" {
  server_id           = tonumber(spinupwp_server.example.id)
  domain              = "example.com"
  site_user           = "example"
  installation_method = "blank"
}
