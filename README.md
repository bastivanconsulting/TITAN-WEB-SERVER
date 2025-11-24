# Custom Proxy Server

A production-ready, high-performance reverse proxy and web server written in Go. This server supports virtual hosting, SSL/TLS (SNI), static file serving, PHP-FPM, and features a built-in management API and Admin UI.

## Features

- **Reverse Proxy**: Forward traffic to backend servers (HTTP/HTTPS).
- **Web Server**: Serve static files and PHP applications (via FastCGI/PHP-FPM).
- **HTTP/3 & QUIC**: Native support for next-generation HTTP/3 protocol via QUIC.
- **SSL/TLS**: Support for HTTPS with SNI (Server Name Indication) using manual certificates.
- **High Security**:
  - **FIPS Compliance**: Supports FIPS 140-2 and 140-3 compliant TLS settings.
  - **HSTS**: Built-in HTTP Strict Transport Security support.
  - **OCSP Stapling**: Automatic fetching and stapling of OCSP responses.
- **Dynamic Configuration**:
  - **Auto-Reload**: Automatically reloads configuration when `config.json` is modified.
  - **API**: Reload routing configuration via protected REST API.
- **Admin UI**: Embedded web interface to manage configuration securely.
- **REST API**: Protected API for automated configuration management.

## Installation

### Prerequisites

- Go 1.24 or later
- PHP-FPM (optional, for PHP support)

### Build

```bash
go build -o proxy-server cmd/server/main.go
```

## Usage

1. Create a `config.json` file (see Configuration section).
2. Start the server:

```bash
./proxy-server -config config.json
```

## Configuration

### Auto-Reload

The server watches the configuration file (default `config.json`) for changes. If you edit and save the file, the server will automatically reload routes and settings without a restart. Note that changing ports (`http_port` / `https_port`) still requires a manual restart.

### FIPS Compliance & High Security Example

Below is a configuration example enabling FIPS compliance (140-3), OCSP Stapling, and HSTS.

```json
{
    "http_port": ":8080",
    "https_port": ":8443",
    "admin_api_key": "your-secret-key-change-me",
    "admin_user": "admin",
    "admin_pass": "secure-password",
    "admin_allowlist": [
        "127.0.0.1",
        "::1"
    ],
    "security": {
        "fips_compliant": true,
        "fips_standard": "140-3"
    },
    "sites": [
        {
            "domain": "secure.example.com",
            "ssl": {
                "enabled": true,
                "provider": "manual",
                "cert_file": "/etc/ssl/certs/secure.example.com.crt",
                "key_file": "/etc/ssl/private/secure.example.com.key",
                "email": "admin@example.com",
                "ocsp_stapling": true,
                "hsts": {
                    "enabled": true,
                    "max_age": 63072000,
                    "include_subdomains": true,
                    "preload": true
                }
            },
            "routes": [
                {
                    "path": "/",
                    "type": "proxy",
                    "proxy_target": "http://localhost:3000"
                }
            ]
        }
    ]
}
```

### Configuration Reference

- **security**:
  - `fips_compliant` (bool): If true, enforces strict TLS security settings.
  - `fips_standard` (string): "140-2" or "140-3". Selects the specific compliance standard (both enforce high-security cipher suites like AES-GCM and ECDHE/RSA).

- **ssl**:
  - `ocsp_stapling` (bool): If true, the server attempts to fetch OCSP status from the issuer and staple it to the handshake.
  - `hsts` (object):
    - `enabled` (bool): Enable HSTS header.
    - `max_age` (int): Duration in seconds.
    - `include_subdomains` (bool): Apply to subdomains.
    - `preload` (bool): Enable preload flag.

## Administration

### Admin UI

Access the Admin UI at: `http://localhost:8080/admin`.
Enter the `admin_api_key` to access.

### REST API

**Endpoint**: `/api/config`
**Headers**: `X-API-Key: <your-key>`

```bash
curl -H "X-API-Key: secret" http://localhost:8080/api/config
```

## License

MIT
