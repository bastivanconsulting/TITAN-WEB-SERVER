#!/bin/bash
set -e

# Couleurs pour les logs
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m' # No Color

log() {
    echo -e "${GREEN}[Titan Setup]${NC} $1"
}

error() {
    echo -e "${RED}[Error]${NC} $1"
    exit 1
}

# Vérifier si l'utilisateur est root
if [ "$EUID" -ne 0 ]; then
  error "Ce script doit être exécuté en tant que root (sudo)."
fi

# Configuration
GO_VERSION="1.24.0"
INSTALL_DIR="/usr/local/sbin"
CONFIG_DIR="/etc/titan"
LOG_DIR="/var/log/titan"
RAFT_DIR="/etc/titan/raft"
SERVICE_FILE="/etc/systemd/system/titan-server.service"
USER="www-data"

log "Mise à jour des paquets et installation des dépendances..."
apt-get update
apt-get install -y curl git gcc libpam0g-dev tar

# Vérification/Installation de Go
if command -v go >/dev/null 2>&1; then
    CURRENT_GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
    log "Go est déjà installé (Version: $CURRENT_GO_VERSION)"
else
    log "Installation de Go $GO_VERSION..."
    curl -OL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"
    rm -rf /usr/local/go && tar -C /usr/local -xzf "go${GO_VERSION}.linux-amd64.tar.gz"
    rm "go${GO_VERSION}.linux-amd64.tar.gz"
    export PATH=$PATH:/usr/local/go/bin

    # Ajouter au PATH de manière permanente si nécessaire
    if ! grep -q "/usr/local/go/bin" /etc/profile; then
        echo "export PATH=\$PATH:/usr/local/go/bin" >> /etc/profile
    fi
fi

# Compilation
log "Compilation de Titan Web Server..."
# On s'assure d'être dans le dossier du script/repo
cd "$(dirname "$0")"

if [ ! -f "go.mod" ]; then
    error "Le fichier go.mod est introuvable. Assurez-vous d'exécuter ce script depuis la racine du dépôt."
fi

go mod tidy
go build -o titan-web-server cmd/server/main.go

if [ ! -f "titan-web-server" ]; then
    error "La compilation a échoué."
fi

# Installation
log "Installation du binaire dans $INSTALL_DIR..."
cp titan-web-server "$INSTALL_DIR/"
chmod +x "$INSTALL_DIR/titan-web-server"

# Configuration
log "Configuration des répertoires et fichiers..."

mkdir -p "$CONFIG_DIR/certs"
mkdir -p "$LOG_DIR"
mkdir -p "$RAFT_DIR"

# Copie du fichier de configuration par défaut s'il n'existe pas
if [ ! -f "$CONFIG_DIR/titan-server.conf" ]; then
    if [ -f "titan-server.conf" ]; then
        cp "titan-server.conf" "$CONFIG_DIR/titan-server.conf"
        # Ajustement des chemins dans le fichier de conf
        sed -i "s|log_dir \".*\";|log_dir \"$LOG_DIR/\";|" "$CONFIG_DIR/titan-server.conf"
        log "Fichier de configuration installé dans $CONFIG_DIR/titan-server.conf"
    else
        log "Attention: titan-server.conf source non trouvé, un fichier vide a été créé."
        touch "$CONFIG_DIR/titan-server.conf"
    fi
else
    log "La configuration existe déjà, elle n'a pas été écrasée."
fi

# Permissions
log "Application des permissions..."
chown -R $USER:$USER "$LOG_DIR"
chown -R $USER:$USER "$RAFT_DIR"

# Permissions de configuration: root propriétaire, www-data peut lire
log "Permissions de configuration: root propriétaire, www-data peut lire..."
chown -R root:$USER "$CONFIG_DIR"
find "$CONFIG_DIR" -type d -exec chmod 750 {} \;
find "$CONFIG_DIR" -type f -exec chmod 640 {} \;

# Permissions des certificats: www-data doit pouvoir écrire pour Let's Encrypt
log "Permissions des certificats: www-data doit pouvoir écrire pour Let's Encrypt..."
CERTS_DIR="$CONFIG_DIR/certs"
chown -R $USER:$USER "$CERTS_DIR"
find "$CERTS_DIR" -type d -exec chmod 750 {} \;
find "$CERTS_DIR" -type f -exec chmod 640 {} \;


# Systemd
log "Création du service Systemd..."
cat <<EOF > "$SERVICE_FILE"
[Unit]
Description=Titan Web Server
After=network.target

[Service]
Type=simple
User=$USER
Group=$USER
ExecStart=$INSTALL_DIR/titan-web-server -config $CONFIG_DIR/titan-server.conf -raft-dir $RAFT_DIR
Restart=on-failure
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

# Activation
log "Activation et démarrage du service..."
systemctl daemon-reload
systemctl enable titan-server
systemctl restart titan-server

log "Installation terminée avec succès !"
log "Vérifiez le statut avec : systemctl status titan-server"
log "Logs disponibles dans : $LOG_DIR"
