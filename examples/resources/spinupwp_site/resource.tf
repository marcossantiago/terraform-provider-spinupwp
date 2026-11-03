data "spinupwp_server" "web" {
  name = "my-server"
}

variable "wp_admin_password" {
  type      = string
  sensitive = true
}

variable "preview_password" {
  type      = string
  sensitive = true
}

resource "spinupwp_site" "blog" {
  server_id           = data.spinupwp_server.web.id
  domain              = "blog.example.com"
  site_user           = "blog"
  installation_method = "wp"
  php_version         = "8.3"
  page_cache_enabled  = true
  cron_interval       = 1

  database = {
    name     = "blog"
    username = "blog"
    # password_wo is optional: SpinupWP generates one if it is unset.
  }

  wordpress = {
    title             = "Example Blog"
    admin_user        = "admin"
    admin_email       = "admin@example.com"
    admin_password_wo = var.wp_admin_password
  }

  # Optional: keep the site private. Bump password_wo_version to send a new password.
  basic_auth = {
    username            = "preview"
    password_wo         = var.preview_password
    password_wo_version = 1
  }

  # Optional: at least one schedule. Removing the block turns backups off.
  backups = {
    storage_provider_id = 34 # Account Settings → Storage Providers
    bucket              = "my-site-backups"
    region              = "nyc3"
    include_database    = true

    daily = {
      hours          = [3]
      retention_days = 14
    }

    weekly = {
      days           = [0] # Sunday
      hour           = 4
      retention_days = 60
    }

    monthly = {
      days           = [{ week = "first", day = 1 }] # first Monday
      hour           = 5
      retention_days = 365
    }
  }

  # A typo in domain or site_user would replace (delete) the site.
  lifecycle {
    prevent_destroy = true
  }
}
