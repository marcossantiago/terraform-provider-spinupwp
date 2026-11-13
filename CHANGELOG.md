## Unreleased

FEATURES:

* `spinupwp_site`: new `public_folder` argument, the folder Nginx serves (e.g. `/public` for Laravel).

BUG FIXES:

* `spinupwp_site`: `database.password_wo` is no longer checked by the provider. Its checks printed the password in their error messages; SpinupWP now checks it.

## 0.2.0 (November 12, 2026)

FEATURES:

* **New resource:** `spinupwp_server`: connect and provision a custom server (any hosting provider) over SSH; removing it deletes the server from SpinupWP only.

## 0.1.0

FEATURES:

* **New resource:** `spinupwp_site`: create, import and delete SpinupWP sites; update PHP version, page cache, cron, basic auth and backups in place.
* **New resource:** `spinupwp_site_https`: Let's Encrypt (webroot or DNS validation) or custom certificates.
* **New data source:** `spinupwp_server`: look up a server by ID or name.
