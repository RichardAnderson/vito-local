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
export VITO_VERSION="3.x"
export VITO_LOCAL_REPO="RichardAnderson/vito-local"
export FRANKENPHP_VERSION="1.11.1"
export PHP_VERSION="8.4.17"
export NODE_VERSION="20.18.1"
export REDIS_VERSION="7.4.2"
export COMPOSER_VERSION="2.8.4"
export VITO_PORT=3000

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

# =============================================================================
# Input Collection
# =============================================================================
echo "Please provide the following configuration values."
echo "Press Enter to accept the default value shown in brackets."
echo ""

# Generate defaults
DEFAULT_V_PASSWORD=$(openssl rand -base64 12)
DEFAULT_VITO_APP_URL="http://localhost:${VITO_PORT}"
DEFAULT_V_ADMIN_EMAIL="admin@vito.local"
DEFAULT_V_ADMIN_PASSWORD=$(openssl rand -base64 12)

# SSH Password for vito user
if [[ -z "${V_PASSWORD}" ]]; then
    printf "SSH password for vito user [%s]: " "${DEFAULT_V_PASSWORD}"
    read V_PASSWORD </dev/tty
    export V_PASSWORD=${V_PASSWORD:-$DEFAULT_V_PASSWORD}
fi
echo "  SSH Password: ${V_PASSWORD}"

# Application URL
if [[ -z "${VITO_APP_URL}" ]]; then
    printf "Application URL [%s]: " "${DEFAULT_VITO_APP_URL}"
    read VITO_APP_URL </dev/tty
    export VITO_APP_URL=${VITO_APP_URL:-$DEFAULT_VITO_APP_URL}
fi
echo "  App URL: ${VITO_APP_URL}"

# Admin email
if [[ -z "${V_ADMIN_EMAIL}" ]]; then
    printf "Admin email address [%s]: " "${DEFAULT_V_ADMIN_EMAIL}"
    read V_ADMIN_EMAIL </dev/tty
    export V_ADMIN_EMAIL=${V_ADMIN_EMAIL:-$DEFAULT_V_ADMIN_EMAIL}
fi
echo "  Admin Email: ${V_ADMIN_EMAIL}"

# Admin password
if [[ -z "${V_ADMIN_PASSWORD}" ]]; then
    printf "Admin password [%s]: " "${DEFAULT_V_ADMIN_PASSWORD}"
    read V_ADMIN_PASSWORD </dev/tty
    export V_ADMIN_PASSWORD=${V_ADMIN_PASSWORD:-$DEFAULT_V_ADMIN_PASSWORD}
fi
echo "  Admin Password: ${V_ADMIN_PASSWORD}"

# Rebuild dependencies
if [[ -z "${REBUILD_DEPS}" ]]; then
    printf "Rebuild all dependencies? (y/N) [N]: "
    read REBUILD_DEPS </dev/tty
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
# Helper Functions
# =============================================================================
log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1"
}

download() {
    local url="$1"
    local dest="$2"
    log "Downloading: ${url}"
    curl -fsSL "${url}" -o "${dest}"
}

# Version tracking directory
VITO_VERSIONS="${VITO_LOCAL}/versions"

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

# =============================================================================
# System Prerequisites (minimal)
# =============================================================================
log "Installing minimal system prerequisites..."
apt-get update
apt-get install -y curl tar xz-utils git unzip build-essential ufw

# =============================================================================
# Create vito user
# =============================================================================
log "Creating vito user..."
if ! id "vito" &>/dev/null; then
    useradd -m -s /bin/bash -p "$(openssl passwd -1 "${V_PASSWORD}")" vito
    echo "vito ALL=(ALL) NOPASSWD:ALL" | tee /etc/sudoers.d/vito
fi

# Create directory structure
mkdir -p "${VITO_BIN}" "${VITO_DATA}" "${VITO_LOGS}" "${VITO_VERSIONS}"
mkdir -p "${VITO_HOME}/.ssh"
chown -R vito:vito "${VITO_HOME}"

# Generate SSH keys for vito user
su - vito -c "ssh-keygen -t rsa -N '' -f ~/.ssh/id_rsa" <<<y 2>/dev/null || true

# =============================================================================
# Install vito-local-service (root service)
# =============================================================================
log "Installing vito-local-service..."
VITO_LOCAL_RELEASE_URL="https://github.com/${VITO_LOCAL_REPO}/releases/latest/download/vito-root-service-linux-${ARCH_SUFFIX}.tar.gz"
VITO_LOCAL_TMP="/tmp/vito-local-service.tar.gz"

download "${VITO_LOCAL_RELEASE_URL}" "${VITO_LOCAL_TMP}"
tar -xzf "${VITO_LOCAL_TMP}" -C /tmp

# Run the vito-local-service installer
if [[ -f /tmp/install.sh ]]; then
    chmod +x /tmp/install.sh
    /tmp/install.sh
fi
rm -f "${VITO_LOCAL_TMP}"

# =============================================================================
# Install FrankenPHP (self-contained PHP + web server)
# =============================================================================
if needs_install "frankenphp" "${FRANKENPHP_VERSION}" "${VITO_BIN}/frankenphp"; then
    log "Installing FrankenPHP ${FRANKENPHP_VERSION}..."
    FRANKENPHP_URL="https://github.com/php/frankenphp/releases/download/v${FRANKENPHP_VERSION}/frankenphp-linux-${FRANKENPHP_ARCH}"
    download "${FRANKENPHP_URL}" "${VITO_BIN}/frankenphp"
    chmod +x "${VITO_BIN}/frankenphp"
    mark_installed "frankenphp" "${FRANKENPHP_VERSION}"
else
    log "FrankenPHP ${FRANKENPHP_VERSION} already installed, skipping..."
fi

# =============================================================================
# Install Static PHP CLI (for composer, artisan, etc.)
# =============================================================================
if needs_install "php" "${PHP_VERSION}" "${VITO_BIN}/php"; then
    log "Installing PHP CLI ${PHP_VERSION}..."
    # Using 'bulk' build which includes intl, redis, and other required extensions
    PHP_URL="https://dl.static-php.dev/static-php-cli/bulk/php-${PHP_VERSION}-cli-linux-${FRANKENPHP_ARCH}.tar.gz"
    PHP_TMP="/tmp/php-cli.tar.gz"
    download "${PHP_URL}" "${PHP_TMP}"
    tar -xzf "${PHP_TMP}" -C "${VITO_BIN}"
    rm -f "${PHP_TMP}"
    chmod +x "${VITO_BIN}/php"
    mark_installed "php" "${PHP_VERSION}"
else
    log "PHP CLI ${PHP_VERSION} already installed, skipping..."
fi

# =============================================================================
# Install Node.js (self-contained)
# =============================================================================
if needs_install "node" "${NODE_VERSION}" "${VITO_LOCAL}/node"; then
    log "Installing Node.js ${NODE_VERSION}..."
    NODE_URL="https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${NODE_ARCH}.tar.xz"
    NODE_TMP="/tmp/node.tar.xz"
    rm -rf "${VITO_LOCAL}/node"
    download "${NODE_URL}" "${NODE_TMP}"
    tar -xJf "${NODE_TMP}" -C "${VITO_LOCAL}"
    mv "${VITO_LOCAL}/node-v${NODE_VERSION}-linux-${NODE_ARCH}" "${VITO_LOCAL}/node"
    rm -f "${NODE_TMP}"

    # Symlink node binaries
    ln -sf "${VITO_LOCAL}/node/bin/node" "${VITO_BIN}/node"
    ln -sf "${VITO_LOCAL}/node/bin/npm" "${VITO_BIN}/npm"
    ln -sf "${VITO_LOCAL}/node/bin/npx" "${VITO_BIN}/npx"
    mark_installed "node" "${NODE_VERSION}"
else
    log "Node.js ${NODE_VERSION} already installed, skipping..."
fi

# =============================================================================
# Install Composer (self-contained)
# =============================================================================
if needs_install "composer" "${COMPOSER_VERSION}" "${VITO_BIN}/composer"; then
    log "Installing Composer ${COMPOSER_VERSION}..."
    COMPOSER_URL="https://getcomposer.org/download/${COMPOSER_VERSION}/composer.phar"
    download "${COMPOSER_URL}" "${VITO_BIN}/composer"
    chmod +x "${VITO_BIN}/composer"
    mark_installed "composer" "${COMPOSER_VERSION}"
else
    log "Composer ${COMPOSER_VERSION} already installed, skipping..."
fi

# =============================================================================
# Install Redis (compiled locally, self-contained)
# =============================================================================
if needs_install "redis" "${REDIS_VERSION}" "${VITO_LOCAL}/redis"; then
    log "Installing Redis ${REDIS_VERSION}..."
    REDIS_URL="https://github.com/redis/redis/archive/refs/tags/${REDIS_VERSION}.tar.gz"
    REDIS_TMP="/tmp/redis.tar.gz"
    REDIS_BUILD="/tmp/redis-${REDIS_VERSION}"

    rm -rf "${VITO_LOCAL}/redis" "${REDIS_BUILD}"
    download "${REDIS_URL}" "${REDIS_TMP}"
    tar -xzf "${REDIS_TMP}" -C /tmp
    cd "${REDIS_BUILD}"
    log "Building Redis... this can take a few minutes"
    make -j"$(nproc)" PREFIX="${VITO_LOCAL}/redis" install > /dev/null 2>&1
    cd /
    rm -rf "${REDIS_TMP}" "${REDIS_BUILD}"

    # Symlink redis binaries
    ln -sf "${VITO_LOCAL}/redis/bin/redis-server" "${VITO_BIN}/redis-server"
    ln -sf "${VITO_LOCAL}/redis/bin/redis-cli" "${VITO_BIN}/redis-cli"
    mark_installed "redis" "${REDIS_VERSION}"
else
    log "Redis ${REDIS_VERSION} already installed, skipping..."
fi

# Create Redis config (always ensure it exists)
cat > "${VITO_DATA}/redis.conf" <<EOF
bind 127.0.0.1
port 6379
daemonize no
dir ${VITO_DATA}
logfile ${VITO_LOGS}/redis.log
pidfile ${VITO_DATA}/redis.pid
EOF

# =============================================================================
# Install Nginx (self-contained static build)
# =============================================================================
NGINX_VERSION="1.27.3"
if needs_install "nginx" "${NGINX_VERSION}" "${VITO_BIN}/nginx"; then
    log "Installing Nginx ${NGINX_VERSION}..."
    NGINX_URL="https://github.com/nginx/nginx/archive/refs/tags/release-${NGINX_VERSION}.tar.gz"
    NGINX_TMP="/tmp/nginx.tar.gz"
    NGINX_BUILD="/tmp/nginx-release-${NGINX_VERSION}"

    rm -rf "${VITO_LOCAL}/nginx" "${NGINX_BUILD}"
    download "${NGINX_URL}" "${NGINX_TMP}"
    tar -xzf "${NGINX_TMP}" -C /tmp

    # Install required build deps for nginx
    apt-get install -y libpcre3-dev zlib1g-dev libssl-dev > /dev/null 2>&1

    cd "${NGINX_BUILD}"
    log "Building Nginx... this can take a few minutes"
    auto/configure \
        --prefix="${VITO_LOCAL}/nginx" \
        --sbin-path="${VITO_BIN}/nginx" \
        --conf-path="${VITO_LOCAL}/nginx/nginx.conf" \
        --error-log-path="${VITO_LOGS}/nginx-error.log" \
        --http-log-path="${VITO_LOGS}/nginx-access.log" \
        --pid-path="${VITO_DATA}/nginx.pid" \
        --with-http_ssl_module \
        --with-http_v2_module \
        --with-http_realip_module \
        --without-http_gzip_module > /dev/null 2>&1
    make -j"$(nproc)" > /dev/null 2>&1
    make install > /dev/null 2>&1
    cd /
    rm -rf "${NGINX_TMP}" "${NGINX_BUILD}"
    mark_installed "nginx" "${NGINX_VERSION}"
else
    log "Nginx ${NGINX_VERSION} already installed, skipping..."
fi

# Create Nginx configuration (always ensure configs are up to date)
mkdir -p "${VITO_LOCAL}/nginx/sites-enabled"
cat > "${VITO_LOCAL}/nginx/nginx.conf" <<EOF
worker_processes auto;
pid ${VITO_DATA}/nginx.pid;
error_log ${VITO_LOGS}/nginx-error.log;

events {
    worker_connections 1024;
}

http {
    include       ${VITO_LOCAL}/nginx/mime.types;
    default_type  application/octet-stream;

    log_format main '\$remote_addr - \$remote_user [\$time_local] "\$request" '
                    '\$status \$body_bytes_sent "\$http_referer" '
                    '"\$http_user_agent"';

    access_log ${VITO_LOGS}/nginx-access.log main;

    sendfile on;
    tcp_nopush on;
    tcp_nodelay on;
    keepalive_timeout 65;
    types_hash_max_size 2048;

    client_max_body_size 100M;

    include ${VITO_LOCAL}/nginx/sites-enabled/*;
}
EOF

# Create Vito site configuration (reverse proxy to FrankenPHP)
cat > "${VITO_LOCAL}/nginx/sites-enabled/vito.conf" <<EOF
server {
    listen ${VITO_PORT};
    listen [::]:${VITO_PORT};
    server_name _;

    root ${VITO_APP}/public;
    index index.php;

    charset utf-8;

    add_header X-Frame-Options "SAMEORIGIN";
    add_header X-Content-Type-Options "nosniff";

    location / {
        try_files \$uri \$uri/ /index.php?\$query_string;
    }

    location = /favicon.ico { access_log off; log_not_found off; }
    location = /robots.txt  { access_log off; log_not_found off; }

    error_page 404 /index.php;

    location ~ \.php\$ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_buffer_size 128k;
        proxy_buffers 4 256k;
        proxy_busy_buffers_size 256k;
    }

    location ~ /\.(?!well-known).* {
        deny all;
    }
}
EOF

# =============================================================================
# Configure Firewall
# =============================================================================
log "Configuring firewall..."
ufw --force reset
ufw default deny incoming
ufw default allow outgoing
ufw allow ssh
ufw allow ${VITO_PORT}/tcp comment 'Vito Web'
ufw --force enable
ufw status verbose

# =============================================================================
# Clone and Setup Vito Application
# =============================================================================
log "Cloning Vito repository..."
export VITO_REPO="https://github.com/vitodeploy/vito.git"

rm -rf "${VITO_APP}"
git config --global core.fileMode false
git clone -b "${VITO_VERSION}" "${VITO_REPO}" "${VITO_APP}"
cd "${VITO_APP}"

# Checkout latest tag
LATEST_TAG=$(git tag -l --merged "${VITO_VERSION}" --sort=-v:refname | head -n 1)
if [[ -n "${LATEST_TAG}" ]]; then
    git checkout "${LATEST_TAG}"
fi

# Set permissions
find "${VITO_APP}" -type d -exec chmod 755 {} \;
find "${VITO_APP}" -type f -exec chmod 644 {} \;
git config core.fileMode false

# Add local bin to PATH for vito user
cat >> "${VITO_HOME}/.bashrc" <<EOF

# Vito local binaries
export PATH="${VITO_BIN}:\${PATH}"
export PATH="${VITO_LOCAL}/node/bin:\${PATH}"
EOF

# Install PHP dependencies using FrankenPHP's embedded PHP
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

su - vito -c "${VITO_BIN}/php ${VITO_APP}/artisan key:generate"
su - vito -c "${VITO_BIN}/php ${VITO_APP}/artisan storage:link"
su - vito -c "${VITO_BIN}/php ${VITO_APP}/artisan migrate --force"
su - vito -c "${VITO_BIN}/php ${VITO_APP}/artisan user:create Vito ${V_ADMIN_EMAIL} ${V_ADMIN_PASSWORD}"

# Generate SSH keys for the application
openssl genpkey -algorithm RSA -out "${VITO_APP}/storage/ssh-private.pem"
chmod 600 "${VITO_APP}/storage/ssh-private.pem"
ssh-keygen -y -f "${VITO_APP}/storage/ssh-private.pem" > "${VITO_APP}/storage/ssh-public.key"
chown vito:vito "${VITO_APP}/storage/ssh-private.pem" "${VITO_APP}/storage/ssh-public.key"

# Optimize
su - vito -c "${VITO_BIN}/php ${VITO_APP}/artisan optimize"

# =============================================================================
# Create systemd services (user-level)
# =============================================================================
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

# FrankenPHP service (PHP application server)
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
ExecStart=${VITO_BIN}/frankenphp php-server --root ${VITO_APP}/public --listen 127.0.0.1:8080
Restart=always
RestartSec=5
Environment=PATH=${VITO_BIN}:${VITO_LOCAL}/node/bin:/usr/local/bin:/usr/bin:/bin

[Install]
WantedBy=multi-user.target
EOF

# Nginx service
cat > /etc/systemd/system/vito-nginx.service <<EOF
[Unit]
Description=Vito Nginx Server
After=network.target vito-php.service
Requires=vito-php.service

[Service]
Type=forking
User=vito
Group=vito
PIDFile=${VITO_DATA}/nginx.pid
ExecStart=${VITO_BIN}/nginx -c ${VITO_LOCAL}/nginx/nginx.conf
ExecReload=/bin/kill -s HUP \$MAINPID
ExecStop=/bin/kill -s QUIT \$MAINPID
Restart=always
RestartSec=5

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

# Reload systemd and enable/start services
systemctl daemon-reload
systemctl enable vito-redis vito-php vito-nginx vito-worker

log "Starting services..."
systemctl start vito-redis
sleep 2
systemctl start vito-php
sleep 2
systemctl start vito-nginx
systemctl start vito-worker

# =============================================================================
# Setup Cron Jobs
# =============================================================================
log "Setting up cron jobs..."
(crontab -u vito -l 2>/dev/null || true; echo "* * * * * ${VITO_BIN}/php ${VITO_APP}/artisan schedule:run >> /dev/null 2>&1") | crontab -u vito -

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
echo "  systemctl status vito-nginx"
echo "  systemctl status vito-worker"
echo ""
echo "Firewall Status:"
ufw status | grep -E "^${VITO_PORT}|^22"
echo ""
echo "Installation paths:"
echo "  App:      ${VITO_APP}"
echo "  Binaries: ${VITO_BIN}"
echo "  Logs:     ${VITO_LOGS}"
echo "  Data:     ${VITO_DATA}"
echo ""
