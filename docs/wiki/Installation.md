Cette page détaille les étapes pour compiler, installer et exécuter Titan Web Server.

## Prérequis

*   **Système d'exploitation** : Linux (recommandé pour la production), macOS, ou Windows.
*   **Go** : Version 1.24 ou supérieure requise.
    *   [Télécharger Go](https://go.dev/dl/)
*   **Git** : Pour cloner le dépôt.

## Compilation

Titan Web Server est distribué sous forme de code source.

1.  **Cloner le dépôt :**
    ```bash
    git clone https://github.com/bastivanconsulting/WebServer.git
    cd WebServer
    ```

2.  **Télécharger les dépendances :**
    ```bash
    go mod tidy
    ```

3.  **Compiler le binaire :**
    ```bash
    go build -o titan-web-server cmd/server/main.go
    ```

    *Note pour Linux avec support PAM :*
    Assurez-vous d'avoir `libpam0g-dev` installé et CGO activé si vous souhaitez utiliser l'authentification PAM pour l'admin.

## Premier Démarrage

Pour démarrer le serveur, vous devez fournir un fichier de configuration. Un fichier d'exemple `titan-server.conf` est fourni à la racine.

```bash
./titan-web-server -config titan-server.conf
```

### Arguments de la ligne de commande

| Argument | Description |
| :--- | :--- |
| `-config` | Chemin vers le fichier de configuration (Défaut: `./titan-server.conf`) |
| `-node-id` | Identifiant unique du nœud pour le clustering (ex: `node1`) |
| `-raft-addr` | Adresse IP et port pour le trafic Raft (ex: `127.0.0.1:9000`) |
| `-raft-dir` | Répertoire de stockage des données Raft (Défaut: `./raft-data`) |
| `-bootstrap` | À utiliser uniquement pour le premier nœud d'un nouveau cluster. |
| `-join` | Adresse d'un nœud existant pour rejoindre un cluster (ex: `127.0.0.1:9000`) |

## Installation en tant que Service (Systemd)

Pour exécuter Titan Server en arrière-plan sur Linux, créez un fichier `/etc/systemd/system/titan-server.service` :

```ini
[Unit]
Description=Titan Web Server
After=network.target

[Service]
Type=simple
User=www-data
Group=www-data
ExecStart=/usr/local/bin/titan-web-server -config /etc/titan/titan-server.conf
Restart=on-failure
# Assurez-vous que le dossier de logs et raft-dir sont accessibles par www-data

[Install]
WantedBy=multi-user.target
```

Activez et démarrez le service :
```bash
sudo systemctl daemon-reload
sudo systemctl enable titan-server
sudo systemctl start titan-server
```
