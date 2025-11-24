# Custom Proxy Server

A production-ready, high-performance reverse proxy and web server written in Go. This server supports virtual hosting, SSL/TLS (SNI), static file serving, PHP-FPM, and features a built-in management API and Admin UI.

## Features

- **Reverse Proxy**: Forward traffic to backend servers (HTTP/HTTPS).
- **Web Server**: Serve static files and PHP applications (via FastCGI/PHP-FPM).
- **HTTP/3 & QUIC**: Native support for next-generation HTTP/3 protocol via QUIC.
- **SSL/TLS**: Support for HTTPS with SNI (Server Name Indication) using manual certificates.
- **High Security**:
  - **FIPS Compliance Mode**: Restricts TLS to FIPS 140-2/3 compliant algorithms and protocols.
  - **HSTS**: Built-in HTTP Strict Transport Security support.
  - **OCSP Stapling**: Automatic fetching and stapling of OCSP responses.
- **Dynamic Configuration**: Reload routing configuration without restarting the server via API.
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

### FIPS Compliance & High Security Example

Below is a configuration example enabling FIPS compliance constraints, OCSP Stapling, and HSTS.

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
        "fips_compliant": true
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
  - `fips_compliant` (bool): If true, enforces TLS 1.2+ and FIPS-approved cipher suites (AES-GCM, ECDSA, RSA).

- **ssl**:
  - `ocsp_stapling` (bool): If true, the server attempts to fetch OCSP status from the issuer and staple it to the handshake.
  - `hsts` (object):
    - `enabled` (bool): Enable HSTS header.
    - `max_age` (int): Duration in seconds (e.g., 63072000 for 2 years).
    - `include_subdomains` (bool): Apply to subdomains.
    - `preload` (bool): Enable preload flag.

### Route Types

- **static**: Serves files from `file_root`.
- **proxy**: Forwards requests to `proxy_target`.
- **php**: Forwards requests to a PHP-FPM server.

## Administration

### Admin UI

Access the Admin UI at: `http://localhost:8080/admin` (or your configured port).
You must enter the `admin_api_key` to load or save configuration.

### REST API

**Endpoint**: `/api/config`

- **GET**: Retrieve current configuration.
- **POST**: Update configuration.

**Headers required**:
- `X-API-Key`: <your-admin-api-key>

```bash
curl -H "X-API-Key: secret" http://localhost:8080/api/config
```

## License

MIT
