# Let's Encrypt certificate. The domain must already point at the server.
resource "spinupwp_site_https" "example" {
  site_id = spinupwp_site.example.id
  type    = "webroot"
}

# Or a certificate validated through a DNS provider configured in SpinupWP,
# which works before the domain points at the server:
#
#   type            = "dns"
#   dns_provider_id = 12
#
# Or your own certificate. Bump private_key_wo_version to send a new key:
#
#   type                   = "custom"
#   certificate            = file("example.com.crt")
#   private_key_wo         = file("example.com.key")
#   private_key_wo_version = 1
