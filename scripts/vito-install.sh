#!/bin/bash
set -e

echo "
 __      ___ _        _____             _
 \ \    / (_) |      |  __ \           | |
  \ \  / / _| |_ ___ | |  | | ___ _ __ | | ___  _   _
   \ \/ / | | __/ _ \| |  | |/ _ \ '_ \| |/ _ \| | | |
    \  /  | | || (_) | |__| |  __/ |_) | | (_) | |_| |
     \/   |_|\__\___/|_____/ \___| .__/|_|\___/ \__, |
                                 | |             __/ |
                                 |_|            |___/

          Self-Contained Local Installation
"

# =============================================================================
# Configuration
# =============================================================================
export VITO_REPO="https://github.com/RichardAnderson/vito"
export VITO_BRANCH="feat/local-install"
export VITO_LOCAL_REPO="RichardAnderson/vito-local"
export FRANKENPHP_VERSION="1.11.1"
export PHP_VERSION="8.4.17"
export NODE_VERSION="20.18.1"
export REDIS_VERSION="7.4.2"
export COMPOSER_VERSION="2.8.4"
DEFAULT_VITO_PORT=3000
DEFAULT_VITO_DOMAIN="localhost"

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)
        ARCH_SUFFIX="amd64"
        NODE_ARCH="x64"
        FRANKENPHP_ARCH="x86_64"
        ;;
    aarch64|arm64)
        ARCH_SUFFIX="arm64"
        NODE_ARCH="arm64"
        FRANKENPHP_ARCH="aarch64"
        ;;
    *)
        echo "Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

# Directories
export VITO_HOME="/home/vito"
export VITO_LOCAL="${VITO_HOME}/.local"
export VITO_BIN="${VITO_LOCAL}/bin"
export VITO_DATA="${VITO_LOCAL}/data"
export VITO_LOGS="${VITO_LOCAL}/logs"
export VITO_APP="${VITO_HOME}/vito"
export VITO_VERSIONS="${VITO_LOCAL}/versions"

# =============================================================================
# Helper Functions
# =============================================================================
log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1"
}

log_error() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] ERROR: $1" >&2
}

# Download with retry logic
download() {
    local url="$1"
    local dest="$2"
    local retries=3
    local delay=5

    log "Downloading: ${url}"
    for ((i=1; i<=retries; i++)); do
        if curl -fsSL "${url}" -o "${dest}"; then
            return 0
        fi
        if [[ $i -lt $retries ]]; then
            log "Download failed, attempt $i/$retries. Retrying in ${delay}s..."
            sleep $delay
        fi
    done
    log_error "Failed to download ${url} after ${retries} attempts"
    return 1
}

# Check if a dependency needs to be installed/rebuilt
# Returns 0 (true) if install needed, 1 (false) if already installed
needs_install() {
    local name="$1"
    local version="$2"
    local check_path="$3"

    # Always rebuild if user requested
    if [[ "${REBUILD_DEPS}" == "Y" ]]; then
        log "  -> ${name}: rebuild requested by user"
        return 0
    fi

    # Check if binary/directory exists
    if [[ ! -e "${check_path}" ]]; then
        log "  -> ${name}: not found at ${check_path}"
        return 0
    fi

    # Check version file
    local version_file="${VITO_VERSIONS}/${name}.version"
    if [[ ! -f "${version_file}" ]]; then
        log "  -> ${name}: no version file at ${version_file}"
        return 0
    fi

    # Compare versions
    local installed_version
    installed_version=$(cat "${version_file}")
    if [[ "${installed_version}" != "${version}" ]]; then
        log "  -> ${name}: version mismatch (installed: ${installed_version}, want: ${version})"
        return 0
    fi

    # Already installed at correct version
    return 1
}

# Mark a dependency as installed
mark_installed() {
    local name="$1"
    local version="$2"
    mkdir -p "${VITO_VERSIONS}"
    echo "${version}" > "${VITO_VERSIONS}/${name}.version"
}

# Wait for a systemd service to become active
wait_for_service() {
    local service="$1"
    local max_wait=30
    local waited=0

    log "Waiting for ${service} to start..."
    while ! systemctl is-active --quiet "${service}"; do
        sleep 1
        ((waited++))
        if [[ $waited -ge $max_wait ]]; then
            log_error "${service} failed to start within ${max_wait}s"
            systemctl status "${service}" --no-pager || true
            return 1
        fi
    done
    log "${service} is running"
}

# Cleanup temporary files
cleanup() {
    log "Cleaning up temporary files..."
    rm -f /tmp/vito-*.tar.gz /tmp/php-cli.tar.gz /tmp/node.tar.xz /tmp/redis.tar.gz 2>/dev/null || true
    rm -rf /tmp/redis-* /tmp/scripts /tmp/systemd /tmp/install.sh /tmp/uninstall.sh 2>/dev/null || true
}

# =============================================================================
# Validation Functions
# =============================================================================
validate_domain() {
    local domain="$1"

    # Check for protocol prefix
    if [[ "${domain}" =~ ^https?:// ]]; then
        log_error "Domain should not include http:// or https://"
        return 1
    fi

    # Check for empty
    if [[ -z "${domain}" ]]; then
        log_error "Domain cannot be empty"
        return 1
    fi

    return 0
}

validate_email() {
    local email="$1"

    # Basic email format check
    if [[ ! "${email}" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]]; then
        log_error "Invalid email format: ${email}"
        return 1
    fi

    return 0
}

validate_port() {
    local port="$1"

    # Check if numeric
    if ! [[ "${port}" =~ ^[0-9]+$ ]]; then
        log_error "Port must be a number"
        return 1
    fi

    # Check if non-privileged
    if [[ "${port}" -lt 1024 ]]; then
        log_error "Port must be >= 1024 to run as non-root user"
        return 1
    fi

    # Check if within valid range
    if [[ "${port}" -gt 65535 ]]; then
        log_error "Port must be <= 65535"
        return 1
    fi

    return 0
}

# Check if domain is valid for SSL (not localhost or IP address)
is_valid_ssl_domain() {
    local domain="$1"

    # Reject localhost
    if [[ "${domain}" == "localhost" ]]; then
        return 1
    fi

    # Reject IP addresses (IPv4)
    if [[ "${domain}" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        return 1
    fi

    # Reject IPv6 addresses (contains colons)
    if [[ "${domain}" == *:* ]]; then
        return 1
    fi

    return 0
}

# =============================================================================
# Installation Functions
# =============================================================================
install_vito_local_service() {
    log "Installing vito-local-service..."
    local release_url="https://github.com/${VITO_LOCAL_REPO}/releases/latest/download/vito-root-service-linux-${ARCH_SUFFIX}.tar.gz"
    local tmp_file="/tmp/vito-local-service.tar.gz"

    download "${release_url}" "${tmp_file}"
    tar -xzf "${tmp_file}" -C /tmp

    # Run the vito-local-service installer
    if [[ -f /tmp/scripts/install.sh ]]; then
        chmod +x /tmp/scripts/install.sh
        /tmp/scripts/install.sh
    elif [[ -f /tmp/install.sh ]]; then
        chmod +x /tmp/install.sh
        /tmp/install.sh
    fi
    rm -f "${tmp_file}"
}

install_frankenphp() {
    if needs_install "frankenphp" "${FRANKENPHP_VERSION}" "${VITO_BIN}/frankenphp"; then
        log "Installing FrankenPHP ${FRANKENPHP_VERSION}..."
        local url="https://github.com/php/frankenphp/releases/download/v${FRANKENPHP_VERSION}/frankenphp-linux-${FRANKENPHP_ARCH}"
        download "${url}" "${VITO_BIN}/frankenphp"
        chmod +x "${VITO_BIN}/frankenphp"
        mark_installed "frankenphp" "${FRANKENPHP_VERSION}"
    else
        log "FrankenPHP ${FRANKENPHP_VERSION} already installed, skipping..."
    fi
}

install_php_cli() {
    if needs_install "php" "${PHP_VERSION}" "${VITO_BIN}/php"; then
        log "Installing PHP CLI ${PHP_VERSION}..."
        # Using 'bulk' build which includes intl, redis, and other required extensions
        local url="https://dl.static-php.dev/static-php-cli/bulk/php-${PHP_VERSION}-cli-linux-${FRANKENPHP_ARCH}.tar.gz"
        local tmp_file="/tmp/php-cli.tar.gz"
        download "${url}" "${tmp_file}"
        tar -xzf "${tmp_file}" -C "${VITO_BIN}"
        rm -f "${tmp_file}"
        chmod +x "${VITO_BIN}/php"
        mark_installed "php" "${PHP_VERSION}"
    else
        log "PHP CLI ${PHP_VERSION} already installed, skipping..."
    fi
}

install_nodejs() {
    if needs_install "node" "${NODE_VERSION}" "${VITO_LOCAL}/node"; then
        log "Installing Node.js ${NODE_VERSION}..."
        local url="https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${NODE_ARCH}.tar.xz"
        local tmp_file="/tmp/node.tar.xz"
        rm -rf "${VITO_LOCAL}/node"
        download "${url}" "${tmp_file}"
        tar -xJf "${tmp_file}" -C "${VITO_LOCAL}"
        mv "${VITO_LOCAL}/node-v${NODE_VERSION}-linux-${NODE_ARCH}" "${VITO_LOCAL}/node"
        rm -f "${tmp_file}"

        # Symlink node binaries
        ln -sf "${VITO_LOCAL}/node/bin/node" "${VITO_BIN}/node"
        ln -sf "${VITO_LOCAL}/node/bin/npm" "${VITO_BIN}/npm"
        ln -sf "${VITO_LOCAL}/node/bin/npx" "${VITO_BIN}/npx"
        mark_installed "node" "${NODE_VERSION}"
    else
        log "Node.js ${NODE_VERSION} already installed, skipping..."
    fi
}

install_composer() {
    if needs_install "composer" "${COMPOSER_VERSION}" "${VITO_BIN}/composer"; then
        log "Installing Composer ${COMPOSER_VERSION}..."
        local url="https://getcomposer.org/download/${COMPOSER_VERSION}/composer.phar"
        download "${url}" "${VITO_BIN}/composer"
        chmod +x "${VITO_BIN}/composer"
        mark_installed "composer" "${COMPOSER_VERSION}"
    else
        log "Composer ${COMPOSER_VERSION} already installed, skipping..."
    fi
}

install_redis() {
    if needs_install "redis" "${REDIS_VERSION}" "${VITO_LOCAL}/redis"; then
        log "Installing Redis ${REDIS_VERSION}..."
        local url="https://github.com/redis/redis/archive/refs/tags/${REDIS_VERSION}.tar.gz"
        local tmp_file="/tmp/redis.tar.gz"
        local build_dir="/tmp/redis-${REDIS_VERSION}"

        rm -rf "${VITO_LOCAL}/redis" "${build_dir}"
        download "${url}" "${tmp_file}"
        tar -xzf "${tmp_file}" -C /tmp

        cd "${build_dir}" || { log_error "Failed to cd to ${build_dir}"; return 1; }
        log "Building Redis (output logged to ${VITO_LOGS}/redis-build.log)..."
        mkdir -p "${VITO_LOGS}"
        if ! make -j"$(nproc)" PREFIX="${VITO_LOCAL}/redis" install >> "${VITO_LOGS}/redis-build.log" 2>&1; then
            log_error "Redis build failed. Check ${VITO_LOGS}/redis-build.log for details"
            return 1
        fi
        cd /
        rm -rf "${tmp_file}" "${build_dir}"

        # Symlink redis binaries
        ln -sf "${VITO_LOCAL}/redis/bin/redis-server" "${VITO_BIN}/redis-server"
        ln -sf "${VITO_LOCAL}/redis/bin/redis-cli" "${VITO_BIN}/redis-cli"
        mark_installed "redis" "${REDIS_VERSION}"
    else
        log "Redis ${REDIS_VERSION} already installed, skipping..."
    fi
}

configure_redis() {
    log "Configuring Redis..."
    cat > "${VITO_DATA}/redis.conf" <<EOF
bind 127.0.0.1
port 6379
daemonize no
dir ${VITO_DATA}
logfile ${VITO_LOGS}/redis.log
pidfile ${VITO_DATA}/redis.pid
EOF
}

# =============================================================================
# SSL Functions
# =============================================================================
install_ssl_prerequisites() {
    if [[ "${ENABLE_SSL}" != "Y" ]]; then return 0; fi

    log "Installing nginx and certbot for SSL..."
    apt-get install -y nginx certbot python3-certbot-nginx

    # Stop nginx temporarily (we'll configure it)
    systemctl stop nginx
}

configure_nginx_acme() {
    if [[ "${ENABLE_SSL}" != "Y" ]]; then return 0; fi

    log "Configuring nginx for Let's Encrypt challenges..."

    cat > /etc/nginx/sites-available/vito-acme <<EOF
server {
    listen 80;
    listen [::]:80;
    server_name ${VITO_DOMAIN};

    # Let's Encrypt ACME challenge only
    location /.well-known/acme-challenge/ {
        root /var/www/html;
    }

    # Return 404 for everything else - nginx only handles ACME
    location / {
        return 404;
    }
}
EOF

    # Enable site
    ln -sf /etc/nginx/sites-available/vito-acme /etc/nginx/sites-enabled/
    rm -f /etc/nginx/sites-enabled/default

    # Test nginx configuration
    if ! nginx -t 2>&1; then
        log_error "Nginx configuration test failed"
        return 1
    fi

    systemctl enable nginx
    systemctl start nginx
}

open_acme_port() {
    if [[ "${ENABLE_SSL}" != "Y" ]]; then return 0; fi

    log "Opening port 80 for ACME challenges..."

    # Ensure ufw is configured
    if ! ufw status | grep -q "Status: active"; then
        ufw default deny incoming
        ufw default allow outgoing
        ufw allow ssh
        ufw --force enable
    fi

    # Open port 80 for Let's Encrypt
    ufw allow 80/tcp comment 'Let'\''s Encrypt ACME'
}

obtain_ssl_certificate() {
    if [[ "${ENABLE_SSL}" != "Y" ]]; then return 0; fi

    log "Obtaining SSL certificate from Let's Encrypt..."

    # Request certificate (non-interactive)
    # Temporarily disable set -e to capture certbot failures
    set +e
    certbot certonly \
        --nginx \
        --non-interactive \
        --agree-tos \
        --email "${V_ADMIN_EMAIL}" \
        --domain "${VITO_DOMAIN}"
    local certbot_exit=$?
    set -e

    if [[ $certbot_exit -ne 0 ]]; then
        log_error "Certbot failed with exit code ${certbot_exit}"
        log_error "Common causes: DNS not pointing to this server, port 80 blocked, rate limited"
        return 1
    fi

    # Verify certificate exists
    if [[ ! -f "/etc/letsencrypt/live/${VITO_DOMAIN}/fullchain.pem" ]]; then
        log_error "Failed to obtain SSL certificate - certificate file not found"
        return 1
    fi

    # Create renewal hook to restart FrankenPHP
    mkdir -p /etc/letsencrypt/renewal-hooks/post
    cat > /etc/letsencrypt/renewal-hooks/post/restart-vito.sh <<EOF
#!/bin/bash
systemctl restart vito-php
EOF
    chmod +x /etc/letsencrypt/renewal-hooks/post/restart-vito.sh

    log "SSL certificate obtained successfully"
}

configure_frankenphp_ssl() {
    if [[ "${ENABLE_SSL}" != "Y" ]]; then return 0; fi

    log "Configuring FrankenPHP with SSL..."

    mkdir -p "${VITO_LOCAL}/etc"
    chown vito:vito "${VITO_LOCAL}/etc"

    cat > "${VITO_LOCAL}/etc/Caddyfile" <<EOF
{
    frankenphp
    # Disable automatic HTTPS (we use certbot certs)
    auto_https off
}

https://${VITO_DOMAIN}:${VITO_PORT} {
    tls /etc/letsencrypt/live/${VITO_DOMAIN}/fullchain.pem /etc/letsencrypt/live/${VITO_DOMAIN}/privkey.pem
    root * ${VITO_APP}/public
    encode zstd gzip
    php_server
}
EOF

    chown vito:vito "${VITO_LOCAL}/etc/Caddyfile"
}

grant_certificate_access() {
    if [[ "${ENABLE_SSL}" != "Y" ]]; then return 0; fi

    log "Granting vito user access to SSL certificates..."

    # Create a dedicated group for certificate access
    local cert_group="acme-certs"
    groupadd -f "${cert_group}"
    usermod -aG "${cert_group}" vito

    # Set group ownership on Let's Encrypt directories
    chgrp -R "${cert_group}" /etc/letsencrypt/live /etc/letsencrypt/archive
    chmod -R g+rx /etc/letsencrypt/live /etc/letsencrypt/archive
}

configure_firewall() {
    log "Configuring firewall..."

    # Enable ufw if not already enabled
    if ! ufw status | grep -q "Status: active"; then
        ufw default deny incoming
        ufw default allow outgoing
    fi

    # Allow SSH (idempotent - ufw handles duplicates)
    ufw allow ssh

    # Allow Vito port
    ufw allow "${VITO_PORT}/tcp" comment 'Vito Web'

    # Port 80 for SSL is already opened by open_acme_port() if SSL enabled

    # Enable firewall
    ufw --force enable
    ufw status verbose
}

setup_vito_user() {
    log "Setting up vito user..."
    if ! id "vito" &>/dev/null; then
        # Use SHA-512 hash instead of MD5 (-6 instead of -1)
        useradd -m -s /bin/bash -p "$(openssl passwd -6 "${V_PASSWORD}")" vito

        # Limited sudo access - only for vito services
        cat > /etc/sudoers.d/vito <<EOF
# Vito user can manage vito services without password
vito ALL=(ALL) NOPASSWD: /bin/systemctl start vito-*
vito ALL=(ALL) NOPASSWD: /bin/systemctl stop vito-*
vito ALL=(ALL) NOPASSWD: /bin/systemctl restart vito-*
vito ALL=(ALL) NOPASSWD: /bin/systemctl status vito-*
vito ALL=(ALL) NOPASSWD: /bin/systemctl enable vito-*
vito ALL=(ALL) NOPASSWD: /bin/systemctl disable vito-*
EOF
        chmod 440 /etc/sudoers.d/vito
    fi

    # Create directory structure
    mkdir -p "${VITO_BIN}" "${VITO_DATA}" "${VITO_LOGS}" "${VITO_VERSIONS}"
    mkdir -p "${VITO_HOME}/.ssh"
    chown -R vito:vito "${VITO_HOME}"

    # Generate SSH keys for vito user (only if not exists)
    if [[ ! -f "${VITO_HOME}/.ssh/id_rsa" ]]; then
        su - vito -c "ssh-keygen -t rsa -N '' -f ~/.ssh/id_rsa" <<<y 2>/dev/null || true
    fi
}

setup_vito_app() {
    log "Cloning Vito repository from ${VITO_REPO} (branch: ${VITO_BRANCH})..."

    rm -rf "${VITO_APP}"
    git config --global core.fileMode false
    git clone -b "${VITO_BRANCH}" "${VITO_REPO}.git" "${VITO_APP}"
    cd "${VITO_APP}" || { log_error "Failed to cd to ${VITO_APP}"; return 1; }

    # Checkout latest tag if available
    local latest_tag
    latest_tag=$(git tag -l --merged "${VITO_BRANCH}" --sort=-v:refname | head -n 1)
    if [[ -n "${latest_tag}" ]]; then
        log "Checking out tag ${latest_tag}..."
        git checkout "${latest_tag}"
    fi

    # Set permissions
    find "${VITO_APP}" -type d -exec chmod 755 {} \;
    find "${VITO_APP}" -type f -exec chmod 644 {} \;
    git config core.fileMode false

    # Add local bin to PATH for vito user (only if not already added)
    if ! grep -q "# Vito local binaries" "${VITO_HOME}/.bashrc" 2>/dev/null; then
        cat >> "${VITO_HOME}/.bashrc" <<EOF

# Vito local binaries
export PATH="${VITO_BIN}:\${PATH}"
export PATH="${VITO_LOCAL}/node/bin:\${PATH}"
EOF
    fi

    # Install PHP dependencies
    log "Installing Composer dependencies..."
    chown -R vito:vito "${VITO_HOME}"
    su - vito -c "cd ${VITO_APP} && PATH=${VITO_BIN}:${VITO_LOCAL}/node/bin:\$PATH COMPOSER_ALLOW_SUPERUSER=1 ${VITO_BIN}/composer install --no-dev --optimize-autoloader"

    # Configure environment
    cp "${VITO_APP}/.env.prod" "${VITO_APP}/.env"
    sed -i "s|^APP_URL=.*|APP_URL=${VITO_APP_URL}|" "${VITO_APP}/.env"

    # Initialize database
    touch "${VITO_APP}/storage/database.sqlite"

    # Fix ownership for files created as root
    chown -R vito:vito "${VITO_APP}"

    # Run artisan commands
    su - vito -c "${VITO_BIN}/php ${VITO_APP}/artisan key:generate"
    su - vito -c "${VITO_BIN}/php ${VITO_APP}/artisan storage:link"
    su - vito -c "${VITO_BIN}/php ${VITO_APP}/artisan migrate --force"

    # Create admin user using environment variables to avoid password in process list
    su - vito -c "V_ADMIN_EMAIL='${V_ADMIN_EMAIL}' V_ADMIN_PASSWORD='${V_ADMIN_PASSWORD}' ${VITO_BIN}/php ${VITO_APP}/artisan user:create Vito \"\${V_ADMIN_EMAIL}\" \"\${V_ADMIN_PASSWORD}\""

    # Generate SSH keys for the application
    openssl genpkey -algorithm RSA -out "${VITO_APP}/storage/ssh-private.pem"
    chmod 600 "${VITO_APP}/storage/ssh-private.pem"
    ssh-keygen -y -f "${VITO_APP}/storage/ssh-private.pem" > "${VITO_APP}/storage/ssh-public.key"
    chown vito:vito "${VITO_APP}/storage/ssh-private.pem" "${VITO_APP}/storage/ssh-public.key"

    # Optimize
    su - vito -c "${VITO_BIN}/php ${VITO_APP}/artisan optimize"
}

configure_systemd() {
    log "Creating systemd services..."

    # Fix ownership before creating services
    chown -R vito:vito "${VITO_HOME}"

    # Redis service (system-level, runs as vito user)
    cat > /etc/systemd/system/vito-redis.service <<EOF
[Unit]
Description=Vito Redis Server
After=network.target

[Service]
Type=simple
User=vito
Group=vito
ExecStart=${VITO_BIN}/redis-server ${VITO_DATA}/redis.conf
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

    # FrankenPHP service - different command for SSL vs non-SSL
    if [[ "${ENABLE_SSL}" == "Y" ]]; then
        FRANKENPHP_CMD="${VITO_BIN}/frankenphp run --config ${VITO_LOCAL}/etc/Caddyfile"
    else
        FRANKENPHP_CMD="${VITO_BIN}/frankenphp php-server --root ${VITO_APP}/public --listen 0.0.0.0:${VITO_PORT}"
    fi

    cat > /etc/systemd/system/vito-php.service <<EOF
[Unit]
Description=Vito FrankenPHP Server
After=network.target vito-redis.service
Requires=vito-redis.service

[Service]
Type=simple
User=vito
Group=vito
WorkingDirectory=${VITO_APP}
ExecStart=${FRANKENPHP_CMD}
Restart=always
RestartSec=5
Environment=PATH=${VITO_BIN}:${VITO_LOCAL}/node/bin:/usr/local/bin:/usr/bin:/bin

[Install]
WantedBy=multi-user.target
EOF

    # Horizon worker service
    cat > /etc/systemd/system/vito-worker.service <<EOF
[Unit]
Description=Vito Horizon Worker
After=network.target vito-redis.service vito-php.service
Requires=vito-redis.service

[Service]
Type=simple
User=vito
Group=vito
WorkingDirectory=${VITO_APP}
ExecStart=${VITO_BIN}/php ${VITO_APP}/artisan horizon
Restart=always
RestartSec=5
Environment=PATH=${VITO_BIN}:${VITO_LOCAL}/node/bin:/usr/local/bin:/usr/bin:/bin

[Install]
WantedBy=multi-user.target
EOF

    # Reload systemd and enable services
    systemctl daemon-reload
    systemctl enable vito-redis vito-php vito-worker
}

start_services() {
    log "Starting services..."

    systemctl start vito-redis
    wait_for_service vito-redis

    systemctl start vito-php
    wait_for_service vito-php

    systemctl start vito-worker
    wait_for_service vito-worker
}

setup_cron() {
    log "Setting up cron jobs..."
    # Remove existing vito schedule entry and add fresh one (prevents duplicates)
    (crontab -u vito -l 2>/dev/null | grep -v "artisan schedule:run" || true; echo "* * * * * ${VITO_BIN}/php ${VITO_APP}/artisan schedule:run >> /dev/null 2>&1") | crontab -u vito -
}

create_local_server() {
    log "Creating local server entry in Vito..."

    # Detect the server's primary IP address
    local server_ip
    server_ip=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{print $7; exit}' || hostname -I | awk '{print $1}')

    if [[ -z "${server_ip}" ]]; then
        log_error "Could not detect server IP address, skipping local server creation"
        return 0
    fi

    # Build the list of open ports
    local ports="22,${VITO_PORT}"
    if [[ "${ENABLE_SSL}" == "Y" ]]; then
        ports="${ports},80"
    fi

    # Determine if nginx is installed
    local nginx_installed="N"
    if [[ "${ENABLE_SSL}" == "Y" ]]; then
        nginx_installed="Y"
    fi

    # SSL flag
    local ssl_enabled="${ENABLE_SSL}"

    # Create the local server (user ID 1 is the admin we just created)
    su - vito -c "${VITO_BIN}/php ${VITO_APP}/artisan servers:create-local '${server_ip}' \
        --ports='${ports}' \
        --nginx='${nginx_installed}' \
        --name='localhost' \
        --user=1 \
        --domain='${VITO_DOMAIN}' \
        --web-port='${VITO_PORT}' \
        --ssl='${ssl_enabled}'"

    log "Local server 'localhost' created with IP ${server_ip}"
}

# =============================================================================
# Acquire Lock (prevent concurrent runs)
# =============================================================================
LOCK_FILE="/var/lock/vito-install.lock"
exec 200>"${LOCK_FILE}"
if ! flock -n 200; then
    log_error "Another installation is already in progress"
    exit 1
fi

# =============================================================================
# Setup cleanup trap
# =============================================================================
trap cleanup EXIT

# =============================================================================
# Input Collection
# =============================================================================
echo "Please provide the following configuration values."
echo "Press Enter to accept the default value shown in brackets."
echo ""

# Generate defaults
DEFAULT_V_PASSWORD=$(openssl rand -base64 12)
DEFAULT_V_ADMIN_EMAIL="admin@vito.local"
DEFAULT_V_ADMIN_PASSWORD=$(openssl rand -base64 12)

# SSH Password for vito user
if [[ -z "${V_PASSWORD}" ]]; then
    printf "SSH password for vito user [auto-generated]: "
    read -r V_PASSWORD </dev/tty
    export V_PASSWORD=${V_PASSWORD:-$DEFAULT_V_PASSWORD}
fi
echo "  SSH Password: [set]"

# Domain
while true; do
    if [[ -z "${VITO_DOMAIN}" ]]; then
        printf "Domain (without http/https) [%s]: " "${DEFAULT_VITO_DOMAIN}"
        read -r VITO_DOMAIN </dev/tty
        VITO_DOMAIN=${VITO_DOMAIN:-$DEFAULT_VITO_DOMAIN}
    fi
    if validate_domain "${VITO_DOMAIN}"; then
        export VITO_DOMAIN
        break
    fi
    unset VITO_DOMAIN
done
echo "  Domain: ${VITO_DOMAIN}"

# Port
while true; do
    if [[ -z "${VITO_PORT}" ]]; then
        printf "Port (must be >= 1024 for non-root) [%s]: " "${DEFAULT_VITO_PORT}"
        read -r VITO_PORT </dev/tty
        VITO_PORT=${VITO_PORT:-$DEFAULT_VITO_PORT}
    fi
    if validate_port "${VITO_PORT}"; then
        export VITO_PORT
        break
    fi
    unset VITO_PORT
done
echo "  Port: ${VITO_PORT}"

# SSL - only ask if domain is valid for SSL (not localhost or IP)
if is_valid_ssl_domain "${VITO_DOMAIN}"; then
    if [[ -z "${ENABLE_SSL}" ]]; then
        printf "Enable SSL with Let's Encrypt? (y/N) [N]: "
        read -r ENABLE_SSL </dev/tty
        ENABLE_SSL=${ENABLE_SSL:-N}
    fi
    if [[ "${ENABLE_SSL}" =~ ^[Yy]$ ]]; then
        export ENABLE_SSL="Y"
        export VITO_APP_URL="https://${VITO_DOMAIN}:${VITO_PORT}"
        echo "  SSL: Enabled"
    else
        export ENABLE_SSL="N"
        export VITO_APP_URL="http://${VITO_DOMAIN}:${VITO_PORT}"
        echo "  SSL: Disabled"
    fi
else
    export ENABLE_SSL="N"
    export VITO_APP_URL="http://${VITO_DOMAIN}:${VITO_PORT}"
fi
echo "  App URL: ${VITO_APP_URL}"

# Admin email
while true; do
    if [[ -z "${V_ADMIN_EMAIL}" ]]; then
        printf "Admin email address [%s]: " "${DEFAULT_V_ADMIN_EMAIL}"
        read -r V_ADMIN_EMAIL </dev/tty
        V_ADMIN_EMAIL=${V_ADMIN_EMAIL:-$DEFAULT_V_ADMIN_EMAIL}
    fi
    if validate_email "${V_ADMIN_EMAIL}"; then
        export V_ADMIN_EMAIL
        break
    fi
    unset V_ADMIN_EMAIL
done
echo "  Admin Email: ${V_ADMIN_EMAIL}"

# Admin password
if [[ -z "${V_ADMIN_PASSWORD}" ]]; then
    printf "Admin password [auto-generated]: "
    read -r V_ADMIN_PASSWORD </dev/tty
    export V_ADMIN_PASSWORD=${V_ADMIN_PASSWORD:-$DEFAULT_V_ADMIN_PASSWORD}
fi
echo "  Admin Password: [set]"

# Rebuild dependencies
if [[ -z "${REBUILD_DEPS}" ]]; then
    printf "Rebuild all dependencies? (y/N) [N]: "
    read -r REBUILD_DEPS </dev/tty
    REBUILD_DEPS=${REBUILD_DEPS:-N}
fi
if [[ "${REBUILD_DEPS}" =~ ^[Yy]$ ]]; then
    export REBUILD_DEPS="Y"
    echo "  Rebuild Dependencies: Yes"
else
    export REBUILD_DEPS="N"
    echo "  Rebuild Dependencies: No (will skip already installed)"
fi

echo ""

# =============================================================================
# Main Installation
# =============================================================================
log "Installing minimal system prerequisites..."
apt-get update
apt-get install -y curl tar xz-utils git unzip build-essential ufw

setup_vito_user

install_vito_local_service
install_frankenphp
install_php_cli
install_nodejs
install_composer
install_redis

install_ssl_prerequisites
configure_nginx_acme
open_acme_port
obtain_ssl_certificate
configure_frankenphp_ssl
grant_certificate_access

configure_redis
configure_firewall

setup_vito_app
configure_systemd
start_services
setup_cron
create_local_server

# =============================================================================
# Final Summary
# =============================================================================
echo ""
echo "========================================"
echo "    Installation Complete!"
echo "========================================"
echo ""
echo "You can access Vito at: ${VITO_APP_URL}"
echo ""
echo "Credentials:"
echo "  SSH User:       vito"
echo "  SSH Password:   ${V_PASSWORD}"
echo "  Admin Email:    ${V_ADMIN_EMAIL}"
echo "  Admin Password: ${V_ADMIN_PASSWORD}"
echo ""
echo "Services:"
echo "  systemctl status vito-redis"
echo "  systemctl status vito-php"
echo "  systemctl status vito-worker"
if [[ "${ENABLE_SSL}" == "Y" ]]; then
    echo "  systemctl status nginx"
fi
echo ""
echo "Firewall Status:"
if [[ "${ENABLE_SSL}" == "Y" ]]; then
    ufw status | grep -E "^${VITO_PORT}|^22|^80"
else
    ufw status | grep -E "^${VITO_PORT}|^22"
fi
echo ""
if [[ "${ENABLE_SSL}" == "Y" ]]; then
    echo "SSL Certificate:"
    echo "  Certificate: /etc/letsencrypt/live/${VITO_DOMAIN}/fullchain.pem"
    echo "  Private Key: /etc/letsencrypt/live/${VITO_DOMAIN}/privkey.pem"
    echo "  Test renewal: certbot renew --dry-run"
    echo ""
fi
echo "Installation paths:"
echo "  App:      ${VITO_APP}"
echo "  Binaries: ${VITO_BIN}"
echo "  Logs:     ${VITO_LOGS}"
echo "  Data:     ${VITO_DATA}"
echo ""
echo "Local Server:"
echo "  A local server entry has been created in Vito."
echo "  You can manage it from the Vito dashboard."
echo ""

# Save credentials to a file for reference (readable only by root)
CREDS_FILE="${VITO_HOME}/.vito-credentials"
cat > "${CREDS_FILE}" <<EOF
# Vito Installation Credentials
# Generated: $(date)
# DELETE THIS FILE AFTER NOTING THE CREDENTIALS

SSH_USER=vito
SSH_PASSWORD=${V_PASSWORD}
ADMIN_EMAIL=${V_ADMIN_EMAIL}
ADMIN_PASSWORD=${V_ADMIN_PASSWORD}
APP_URL=${VITO_APP_URL}
SSL_ENABLED=${ENABLE_SSL}
EOF
chmod 600 "${CREDS_FILE}"
chown root:root "${CREDS_FILE}"
echo "Credentials saved to: ${CREDS_FILE} (root access only)"
echo "Please save these credentials and delete the file."
echo ""
