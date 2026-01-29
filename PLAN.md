# Plan: Let's Encrypt SSL Support for Vito Installer

## Overview

Add an option to the installer to enable automatic SSL certificates via Let's Encrypt using FrankenPHP's built-in Caddy server.

## Research Findings

### How FrankenPHP/Caddy Handles SSL

1. **Automatic HTTPS**: FrankenPHP (built on Caddy) has automatic HTTPS built-in with Let's Encrypt
2. **Configuration**: Simply use a Caddyfile with the domain name - Caddy handles certificate provisioning automatically
3. **ACME Challenge**: Uses HTTP-01 challenge (port 80) or TLS-ALPN-01 (port 443)
4. **No Global Packages**: Everything is built into the FrankenPHP binary - no certbot or other tools needed
5. **Storage**: Certificates stored in `$HOME/.local/share/caddy` (must be persistent)

### Challenge: Privileged Ports

SSL requires ports 80 (for ACME challenge/redirect) and 443 (for HTTPS). These are privileged ports (< 1024) that normally require root.

**Solution**: Use `setcap` to grant `CAP_NET_BIND_SERVICE` capability to FrankenPHP binary:
```bash
sudo setcap 'cap_net_bind_service=+ep' /home/vito/.local/bin/frankenphp
```

This allows the binary to bind to ports 80/443 without running as root.

## Implementation Plan

### 1. Add New Input Questions

After asking for domain and port, add:

```
Enable Let's Encrypt SSL? (y/N) [N]:
```

If SSL is enabled:
- Port question becomes irrelevant (will use 80/443)
- Domain MUST be a real domain (not localhost/IP)
- Validate domain is not localhost or IP address

### 2. Create Caddyfile for SSL Mode

When SSL is enabled, create `/home/vito/.local/etc/Caddyfile`:

```caddyfile
{
    frankenphp
}

example.com {
    root * /home/vito/vito/public
    php_server
}
```

### 3. Update FrankenPHP Service

**HTTP Mode (current)**:
```
ExecStart=/home/vito/.local/bin/frankenphp php-server --root /home/vito/vito/public --listen 0.0.0.0:3000
```

**SSL Mode**:
```
ExecStart=/home/vito/.local/bin/frankenphp run --config /home/vito/.local/etc/Caddyfile
```

### 4. Grant Port Binding Capability

For SSL mode, add after FrankenPHP installation:
```bash
setcap 'cap_net_bind_service=+ep' ${VITO_BIN}/frankenphp
```

### 5. Update Firewall Rules

**HTTP Mode**:
```bash
ufw allow ${VITO_PORT}/tcp
```

**SSL Mode**:
```bash
ufw allow 80/tcp   # For ACME challenge and HTTP->HTTPS redirect
ufw allow 443/tcp  # For HTTPS
```

### 6. Update APP_URL

**HTTP Mode**:
```
APP_URL=http://{domain}:{port}
```

**SSL Mode**:
```
APP_URL=https://{domain}
```

### 7. Ensure Certificate Storage

Create and set permissions on Caddy data directory:
```bash
mkdir -p /home/vito/.local/share/caddy
chown -R vito:vito /home/vito/.local/share/caddy
```

## Configuration Matrix

| Setting | HTTP Mode | SSL Mode |
|---------|-----------|----------|
| Port | User-defined (default 3000) | 80 + 443 |
| APP_URL | `http://{domain}:{port}` | `https://{domain}` |
| FrankenPHP Command | `php-server --listen 0.0.0.0:{port}` | `run --config Caddyfile` |
| Firewall | Allow {port} | Allow 80, 443 |
| setcap | Not needed | Required |
| Domain validation | Allow localhost/IP | Must be real domain |

## Input Flow

```
Domain (without http/https) [localhost]: example.com
  Domain: example.com

Enable Let's Encrypt SSL? (y/N) [N]: y
  SSL: Enabled (ports 80/443)
  App URL: https://example.com

# Port question skipped when SSL enabled
```

Or without SSL:
```
Domain (without http/https) [localhost]: example.com
  Domain: example.com

Enable Let's Encrypt SSL? (y/N) [N]:
  SSL: Disabled

Port (must be >= 1024 for non-root) [3000]:
  Port: 3000
  App URL: http://example.com:3000
```

## Validation Rules

1. If SSL enabled, domain cannot be:
   - `localhost`
   - `127.0.0.1` or any IP address
   - Empty

2. If SSL disabled, port must be >= 1024

## Files to Modify

1. `scripts/vito-install.sh`:
   - Add SSL question
   - Conditional logic for SSL vs HTTP mode
   - Create Caddyfile when SSL enabled
   - Update systemd service based on mode
   - Update firewall rules based on mode
   - Run setcap when SSL enabled

## Risks and Considerations

1. **setcap persistence**: Package updates may overwrite the binary, removing the capability. The installer should handle re-runs gracefully.

2. **DNS propagation**: User must ensure DNS is configured before running installer with SSL, otherwise ACME challenge will fail.

3. **Rate limits**: Let's Encrypt has rate limits. Failed attempts should be logged clearly.

4. **Certificate renewal**: Caddy handles this automatically, but the service must be running.

## Sources

- [Caddy Automatic HTTPS](https://caddyserver.com/docs/automatic-https)
- [FrankenPHP Production Docs](https://frankenphp.dev/docs/production/)
- [FrankenPHP Configuration](https://frankenphp.dev/docs/config/)
- [Linux CAP_NET_BIND_SERVICE](https://www.baeldung.com/linux/bind-process-privileged-port)
