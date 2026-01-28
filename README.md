<div align="center">

# Vito Local Root Service

**Secure root command execution for single-server [VitoDeploy](https://github.com/vitodeploy/vito) deployments**

[![Build](https://github.com/RichardAnderson/vito-local/actions/workflows/build.yml/badge.svg)](https://github.com/RichardAnderson/vito-local/actions/workflows/build.yml)
[![Release](https://img.shields.io/github/v/release/RichardAnderson/vito-local)](https://github.com/RichardAnderson/vito-local/releases/latest)
[![Go](https://img.shields.io/github/go-mod/go-version/RichardAnderson/vito-local)](go.mod)
[![Platform](https://img.shields.io/badge/platform-linux-blue)]()

---

</div>

A companion service for [VitoDeploy](https://github.com/vitodeploy/vito) that enables single-server deployments. Instead of requiring a dedicated server to host VitoDeploy and a separate server for your sites, the root service lets VitoDeploy manage the same server it runs on by providing a secure channel for executing privileged commands.

VitoDeploy's PHP application runs as an unprivileged user (`vito`), but server management tasks — installing packages, configuring Nginx, managing systemd services — require root. This daemon bridges that gap: it listens on a Unix socket, authenticates the caller via the Linux kernel's `SO_PEERCRED` mechanism, and executes commands as root while streaming output back as newline-delimited JSON.

## Architecture

```
VitoDeploy (PHP, runs as "vito" user)
    │
    │  Unix socket connection
    ▼
/run/vito-root.sock (root:vito 0660)
    │
    │  SO_PEERCRED UID verification
    ▼
vito-root-service (runs as root)
    │
    ├── Parse JSON request
    ├── Filter environment variables (blocklist enforced)
    ├── Execute: /bin/bash -c <command>
    └── Stream: stdout/stderr/exit as NDJSON
```

**Connection model:** One connection = one command. The client connects, sends a single JSON request, receives a stream of JSON responses, and the connection closes after the command completes.

**Authentication:** The Linux kernel's `SO_PEERCRED` socket option provides the connecting process's UID, verified at the kernel level — it cannot be spoofed by userspace. Only the configured user (default: `vito`) is permitted to connect.

**Socket activation:** The service integrates with systemd socket activation. The socket is created by systemd and the daemon is started on-demand when VitoDeploy first connects, keeping resource usage at zero when idle.

## Installation

### Quick Install

Download and install the latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/RichardAnderson/vito-local/main/scripts/install.sh | sudo bash
```

To install for a user other than `vito`:

```bash
curl -fsSL https://raw.githubusercontent.com/RichardAnderson/vito-local/main/scripts/install.sh | sudo VITO_USER=myuser bash
```

To install a specific version:

```bash
curl -fsSL https://raw.githubusercontent.com/RichardAnderson/vito-local/main/scripts/install.sh | sudo VITO_VERSION=v1.0.0 bash
```

### Manual Install

Download the appropriate release archive from [GitHub Releases](https://github.com/RichardAnderson/vito-local/releases), then:

```bash
tar xzf vito-root-service-v1.0.0-linux-amd64.tar.gz
sudo install -m 0755 vito-root-service /usr/local/bin/
sudo install -m 0644 systemd/vito-root.socket /etc/systemd/system/
sudo install -m 0644 systemd/vito-root.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now vito-root.socket
```

### Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/RichardAnderson/vito-local/main/scripts/uninstall.sh | sudo bash
```

Or manually:

```bash
sudo systemctl stop vito-root.socket vito-root.service
sudo systemctl disable vito-root.socket vito-root.service
sudo rm -f /usr/local/bin/vito-root-service
sudo rm -f /etc/systemd/system/vito-root.socket /etc/systemd/system/vito-root.service
sudo rm -f /run/vito-root.sock
sudo systemctl daemon-reload
```

## Configuration

The service is configured via command-line flags in the systemd unit file. Edit `/etc/systemd/system/vito-root.service` to change defaults.

| Flag | Default | Description |
|------|---------|-------------|
| `-socket` | `/run/vito-root.sock` | Unix socket path |
| `-user` | `vito` | Allowed connecting user |
| `-max-exec-timeout` | `0` (no limit) | Maximum command execution time (e.g., `5m`, `1h`) |
| `-max-connections` | `100` | Maximum concurrent connections |
| `-log-level` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `-log-json` | `false` | Output structured JSON logs |
| `-version` | | Print version and exit |

## Protocol

### Request (client → server)

A single newline-delimited JSON object:

```json
{"command": "systemctl restart nginx", "env": {"DEBIAN_FRONTEND": "noninteractive"}, "cwd": "/tmp"}
```

| Field | Required | Description |
|-------|----------|-------------|
| `command` | Yes | Shell command to execute via `/bin/bash -c` |
| `env` | No | Additional environment variables (dangerous vars like `LD_PRELOAD` and `PATH` are blocked) |
| `cwd` | No | Working directory for the command |

Requests are limited to 10 MB.

### Response (server → client)

A stream of newline-delimited JSON objects:

```json
{"type": "stdout", "data": "● nginx.service - A high performance web server\n"}
{"type": "stderr", "data": "Warning: something\n"}
{"type": "exit", "code": 0}
```

| Type | Fields | Description |
|------|--------|-------------|
| `stdout` | `data` | Standard output chunk |
| `stderr` | `data` | Standard error chunk |
| `exit` | `code` | Command completed; `code` is the exit code |
| `error` | `message` | Protocol or execution error |

The stream always terminates with either an `exit` or `error` response.

## PHP Usage

Connect to the socket, send a JSON request, and read the NDJSON response stream:

```php
$sock = stream_socket_client('unix:///run/vito-root.sock', $errno, $errstr, 5);
fwrite($sock, json_encode(['command' => 'systemctl restart nginx']) . "\n");

while ($line = fgets($sock)) {
    $msg = json_decode(trim($line), true);
    match ($msg['type']) {
        'stdout' => print($msg['data']),
        'stderr' => fwrite(STDERR, $msg['data']),
        'exit'   => break,
        'error'  => throw new RuntimeException($msg['message']),
    };
}
fclose($sock);
```

Pass environment variables or a working directory:

```php
$request = [
    'command' => 'apt-get install -y nginx',
    'env'     => ['DEBIAN_FRONTEND' => 'noninteractive'],
    'cwd'     => '/tmp',
];
fwrite($sock, json_encode($request) . "\n");
```

Stream output into a database (e.g. to log a deployment):

```php
$sock = stream_socket_client('unix:///run/vito-root.sock', $errno, $errstr, 5);
fwrite($sock, json_encode(['command' => 'apt-get update']) . "\n");

$log = '';
while ($line = fgets($sock)) {
    $msg = json_decode(trim($line), true);
    match ($msg['type']) {
        'stdout', 'stderr' => $log .= $msg['data'],
        'exit' => DB::table('deployment_logs')->insert([
            'command'   => 'apt-get update',
            'output'    => $log,
            'exit_code' => $msg['code'],
        ]),
        'error' => DB::table('deployment_logs')->insert([
            'command'   => 'apt-get update',
            'output'    => $msg['message'],
            'exit_code' => -1,
        ]),
    };
    if ($msg['type'] === 'exit' || $msg['type'] === 'error') break;
}
fclose($sock);
```

## Security

This service runs as root and executes arbitrary shell commands. Its security model relies on multiple layers:

- **Kernel-level authentication**: `SO_PEERCRED` provides peer credentials verified by the Linux kernel. The UID cannot be forged by userspace processes.
- **UID authorization**: Only the configured system user may connect. All other connections are rejected before any command processing.
- **Socket permissions**: The socket file is created as `root:<vito-group>` with mode `0660`, providing filesystem-level access control in addition to `SO_PEERCRED`.
- **Environment variable blocklist**: Clients cannot set dangerous variables (`LD_PRELOAD`, `LD_LIBRARY_PATH`, `PATH`, `BASH_ENV`, `IFS`, and all `LD_*`/`BASH_FUNC_*` prefixes).
- **Request size limit**: Requests are capped at 10 MB to prevent memory exhaustion.
- **Connection limit**: Concurrent connections are bounded (default: 100) to prevent resource exhaustion.
- **Graceful process management**: On cancellation, child processes receive `SIGTERM` (not `SIGKILL`) with a 5-second grace period, and signals are sent to the entire process group to prevent orphans.
- **Audit logging**: Every command is logged with the peer's UID, PID, command string, working directory, and exit code.
- **Systemd hardening**: The service unit includes `ProtectSystem=strict`, `ProtectHome=read-only`, `PrivateTmp=true`, `ProtectKernelTunables=true`, `ProtectKernelModules=true`, `ProtectControlGroups=true`, `RestrictNamespaces=true`, and process/task limits.

**Trust boundary**: The security of this system depends on the security of the allowed user account. Any process running as that user has full root command execution capability through this service. Ensure the `vito` user account and the VitoDeploy application are properly secured.

## Development

### Prerequisites

- Go 1.24+
- golangci-lint v2 (optional, for linting)
- Ubuntu (or any Linux distribution for full `SO_PEERCRED` support)

### Build

```bash
go build -o bin/vito-root-service ./cmd/vito-root-service
```

### Test

```bash
go test -v -race ./...
```

### Project Structure

```
cmd/vito-root-service/     Entry point, CLI flags, signal handling
internal/
  config/                  Configuration and user lookup
  protocol/                Request/Response types, NDJSON serialization
  executor/                Command execution with streaming callbacks
  server/                  Socket listener, SO_PEERCRED auth, connection handler
systemd/                   Socket and service unit files
scripts/                   Install/uninstall scripts
```
