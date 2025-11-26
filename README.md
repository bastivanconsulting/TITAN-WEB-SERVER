# TITAN WEB SERVER v1.25.1

**Titan Web Server** est une solution de serveur web et reverse proxy haute performance, conçu pour la simplicité et l'extensibilité.

---

## 📋 Prérequis

*   **Système d'exploitation** : Linux, macOS, ou Windows.
*   **Langage** : Go **1.24** ou supérieur.
    *   [Télécharger Go](https://go.dev/dl/)

---

## 🛠️ Installation et Compilation

1.  **Cloner le dépôt** :
    ```bash
    git clone https://github.com/bastivanconsulting/WebServer.git
    cd WebServer
    ```

2.  **Compiler le binaire** :
    ```bash
    go mod tidy
    go build -o titan-web-server cmd/server/main.go
    ```

---

## ⚙️ Configuration

Titan Server utilise un format de configuration de type Nginx.

### Structure des Fichiers
*   **Fichier Principal** : `titan-server.conf`
*   **Modules** : `conf.d/`
*   **Sites** : `sites-enabled/`

### Exemple de `titan-server.conf`

```nginx
# Ports d'écoute
http_port :8080;
https_port :8443;

# Logs
log_dir "/var/log/titan/";

# Sécurité Globale
security {
    fips_compliant on;
}
```

### Exemple de fichier de site dans `sites-enabled/`

```nginx
server {
    server_name www.example.com;

    location / {
        root /var/www/html;
    }
}
```

---

## 🚀 Modules JavaScript

Étendez les fonctionnalités de Titan avec des modules JavaScript. [Voir la documentation des modules](docs/modules.md).

---

## 📖 Directives de Location

### `auth_basic`

Active l'authentification HTTP Basic pour une `location`.

*   **Syntaxe** : `auth_basic "Message"`
*   **Contexte** : `location`

### `auth_basic_user_file`

Spécifie le fichier `.htpasswd` à utiliser pour l'authentification.

*   **Syntaxe** : `auth_basic_user_file /chemin/vers/le/fichier`
*   **Contexte** : `location`

### `proxy_ssl_verify`

Active ou désactive la vérification des certificats SSL pour les `proxy_pass` HTTPS.

*   **Syntaxe** : `proxy_ssl_verify on|off`
*   **Défaut** : `on`
*   **Contexte** : `location`

### `limit_rate`

Limite la vitesse de téléchargement pour une `location`.

*   **Syntaxe** : `limit_rate <vitesse>k` (en Ko/s)
*   **Contexte** : `location`

---

## 🤝 Contribuer

Les contributions sont les bienvenues !

### Lancer les tests
```bash
go test ./...
```

### Proposer des changements
1.  Forkez le dépôt.
2.  Créez une nouvelle branche.
3.  Faites vos changements.
4.  Ouvrez une Pull Request.
