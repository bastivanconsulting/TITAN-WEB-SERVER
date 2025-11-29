#!/bin/bash
if ! getent group titan > /dev/null; then
    groupadd -r titan
fi
if ! getent passwd titan > /dev/null; then
    useradd -r -g titan -d /var/lib/titan-server -s /sbin/nologin -c "Titan Web Server" titan
fi
# Ensure directories exist
mkdir -p /var/log/titan /etc/titan /var/lib/titan-server

# Set permissions
chown -R titan:titan /var/log/titan /var/lib/titan-server
# Config dir is usually root owned but readable
chmod 755 /var/log/titan
chmod 755 /var/lib/titan-server

systemctl daemon-reload
# Don't enable by default, let admin do it? Or enable by default? Debian policy says start on install.
# Usually:
if [ "$1" = "configure" ] || [ "$1" = "install" ]; then
    systemctl enable titan-web-server.service || true
    systemctl start titan-web-server.service || true
fi
