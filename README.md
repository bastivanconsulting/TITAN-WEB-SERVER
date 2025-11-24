# Custom Proxy Server

A production-ready, high-performance reverse proxy and web server written in Go. This server supports virtual hosting, SSL/TLS (SNI), static file serving, PHP-FPM, and features a built-in management API and Admin UI.

## Features

- **Reverse Proxy**: Forward traffic to backend servers (HTTP/HTTPS).
- **Web Server**: Serve static files and PHP applications (via FastCGI/PHP-FPM).
- **HTTP/3 & QUIC**: Native support for next-generation HTTP/3 protocol via QUIC.
- **SSL/TLS**: Support for HTTPS with SNI (Server Name Indication) using manual certificates.
- **Dynamic Configuration**: Reload routing configuration without restarting the server via API. (Note: Port changes require a restart).
- **Admin UI**: Embedded web interface to manage configuration securely.
- **REST API**: Protected API for automated configuration management.
- **Security**: IP Allowlisting and API Key protection for administrative endpoints.
- **Custom Error Pages**: Classy, Google-style minimal error pages.

## Installation

### Prerequisites

- Go 1.22 or later
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

By default, it looks for `config.json` in the current directory.

## Configuration

The configuration is a JSON file structure. Below is a comprehensive example showing all available options:

```json
{
    "http_port": ":8080",
    "https_port": ":8443",
    "admin_api_key": "your-secret-key-change-me",
    "admin_user": "admin",
    "admin_pass": "secure-password",
    "admin_allowlist": [
        "127.0.0.1",
        "::1",
        "192.168.1.100"
    ],
    "cache": {
        "type": "disk",
        "path": "/tmp/proxy-cache"
    },
    "sites": [
        {
            "domain": "example.com",
            "ssl": {
                "enabled": true,
                "provider": "manual",
                "cert_file": "/etc/ssl/certs/example.com.crt",
                "key_file": "/etc/ssl/private/example.com.key",
                "email": "admin@example.com"
            },
            "routes": [
                {
                    "path": "/api",
                    "type": "proxy",
                    "proxy_target": "http://localhost:3000"
                },
                {
                    "path": "/assets",
                    "type": "static",
                    "file_root": "/var/www/html/assets"
                },
                {
                    "path": "/",
                    "type": "static",
                    "file_root": "/var/www/html"
                }
            ]
        },
        {
            "domain": "app.local",
            "routes": [
                {
                    "path": "/",
                    "type": "php",
                    "proxy_target": "tcp://127.0.0.1:9000",
                    "file_root": "/var/www/app"
                }
            ]
        },
        {
            "domain": "memory-cache.local",
            "ssl": {
                "enabled": false
            },
            "routes": [
                {
                    "path": "/",
                    "type": "proxy",
                    "proxy_target": "http://localhost:8081"
                }
            ]
        }
    ]
}
```

### Route Types

- **static**: Serves files from `file_root`.
- **proxy**: Forwards requests to `proxy_target`.
- **php**: Forwards requests to a PHP-FPM server (specified in `proxy_target`, e.g., `tcp://127.0.0.1:9000` or `unix:///var/run/php/php-fpm.sock`).

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

**Example**:

```bash
curl -H "X-API-Key: secret" http://localhost:8080/api/config
```

## Authentication

A `/login` endpoint is available for integration with external tools or future UI enhancements. It validates username/password against the internal provider (currently simple/mock).

```bash
curl -X POST -d "username=admin&password=admin123" http://localhost:8080/login
```

## License

MIT
