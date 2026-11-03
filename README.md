# Terraform Provider for SpinupWP

Manage [SpinupWP](https://spinupwp.com) sites with Terraform, through the [SpinupWP REST API](https://api.spinupwp.com).

| Resource | Manages |
|---|---|
| `spinupwp_site` | Sites, with their PHP version, page cache, cron, basic auth and backups |
| `spinupwp_site_https` | Let's Encrypt (webroot or DNS) or a custom certificate |
| `spinupwp_server` (data source) | Look up a server by ID or name |

Documentation: [docs/](docs/) (published on the Terraform Registry).

## Usage

Requires Terraform 1.11+.

```hcl
terraform {
  required_providers {
    spinupwp = {
      source  = "marcossantiago/spinupwp"
      version = "~> 0.1"
    }
  }
}

provider "spinupwp" {} # reads SPINUPWP_API_TOKEN

data "spinupwp_server" "web" {
  name = "my-server"
}

resource "spinupwp_site" "example" {
  server_id           = data.spinupwp_server.web.id
  domain              = "example.com"
  site_user           = "example"
  installation_method = "blank"
  php_version         = "8.3"
}
```

Creating a site waits until SpinupWP has deployed it. `server_id`, `domain`, `site_user` and the create-only arguments replace the site when changed; see the [site docs](docs/resources/site.md) before managing sites you care about.

## Development

Requires Go (see `go.mod`) and Terraform 1.11+.

| Command | What it does |
|---|---|
| `make build` | Build the provider |
| `make test` | Unit tests. They run Terraform against an in-memory fake SpinupWP API, so no credentials are needed |
| `make testacc` | Acceptance tests against the real API (see below) |
| `make lint` | Run [golangci-lint](https://golangci-lint.run) |
| `make generate` | Regenerate `docs/` from the schema, `examples/` and `templates/` |

### Acceptance tests

They create real sites named `tfacc<random>.example.com` on the server, and delete them with their databases and backups.

```sh
export SPINUPWP_API_TOKEN=...                 # token with write access
export SPINUPWP_ACC_SERVER=my-server          # a server the tests may create sites on
# Optional, each enables more tests:
export SPINUPWP_ACC_SITE_ID=12345             # an existing site, only read
export SPINUPWP_ACC_STORAGE_PROVIDER_ID=34    # backups
export SPINUPWP_ACC_BUCKET=my-backups
export SPINUPWP_ACC_REGION=nyc3
export SPINUPWP_ACC_WEBROOT_DOMAIN=test.example.com  # Let's Encrypt; must point at the server
make testacc
```

### Using a local build

```hcl
# ~/.terraformrc
provider_installation {
  dev_overrides {
    "marcossantiago/spinupwp" = "/path/to/go/bin"
  }
  direct {}
}
```

Then `make install` and run Terraform as usual (skip `terraform init`).

## Releasing

Push a `vX.Y.Z` tag. The [release workflow](.github/workflows/release.yml) builds and signs the release with GoReleaser; the Terraform Registry picks it up. It needs the `GPG_PRIVATE_KEY` and `PASSPHRASE` repository secrets, and the matching public key added to the registry.

## License

[MPL-2.0](LICENSE)
