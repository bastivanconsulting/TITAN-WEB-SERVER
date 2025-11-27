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
    git clone git@github.com:bastivanconsulting/TITAN-WEB-SERVER.git
    cd TITAN-WEB-SERVER
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
# Le serveur écoutera sur toutes les interfaces, y compris IPv4 et IPv6.
http_port :8080;
https_port :8443;

# Pour écouter uniquement sur une adresse IPv6 spécifique :
# http_port [::1]:8080;

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

## 🔒 Configuration SSL

Titan prend en charge SSL/TLS sans modifier le fichier de configuration.
Dossier : `/etc/titan/certs/manual/`
### Redirection HTTPS Automatique

Si un site est configuré avec SSL, le serveur redirigera automatiquement les requêtes HTTP vers HTTPS avec une redirection permanente (301).

---

##  htaccess

Titan prend en charge un sous-ensemble des directives des fichiers `.htaccess` pour les routes statiques et PHP. Placez simplement un fichier `.htaccess` à la racine de votre site.

### Directives prises en charge

*   `RewriteRule`
*   `RewriteCond`

---

## 🚀 Modules JavaScript

Étendez les fonctionnalités de Titan avec des modules JavaScript. [Voir la documentation des modules](docs/modules.md).

---

## 🛡️ WAF (Web Application Firewall)

Le WAF protège vos applications contre les attaques courantes.

### Directives du WAF

*   `waf on|off;`
*   `waf_wp on|off;` (Protection WordPress)
*   `waf_joomla on|off;` (Protection Joomla)
*   `waf_php on|off;` (Protection PHP)

---

## ⚡️ Performance

### Compression GZIP

Activez la compression GZIP pour réduire la taille des réponses.

*   **Syntaxe** : `gzip on|off;`
*   **Contexte** : `server`

### Mise en Cache

Titan prend en charge la mise en cache en mémoire et sur disque.

*   **Syntaxe** :
    ```nginx
    cache {
        type memory|disk;
        path /chemin/vers/le/cache;
    }
    ```
*   **Contexte** : `http`

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

## ⚖️ Load Balancing

Titan supporte le load balancing pour les routes `proxy`.

### Algorithmes

*   `round_robin` (défaut) : Distribue les requêtes de manière circulaire.
*   `least_conn` : Envoie les nouvelles requêtes au serveur avec le moins de connexions actives.
*   `ip_hash` : Assure que les requêtes d'une même adresse IP client sont dirigées vers le même serveur.

### Health Checks

*   `web` : Effectue une requête HTTP GET sur une URL spécifiée.
*   `tcp` : Tente d'établir une connexion TCP avec le serveur.

### Exemple de configuration

```nginx
location /api/ {
    upstreams http://backend1:8080 http://backend2:8080;

    load_balancer {
        algorithm least_conn;
        health_check {
            type web;
            path "/health";
            interval 5s;
            timeout 2s;
        }
    }
}
```

---

## 🚀 Déploiement

Déployez Titan Web Server en utilisant Docker ou Kubernetes.

### Docker

1.  **Construire l'image Docker** :
    ```bash
    docker build -t titan-web-server:latest .
    ```

2.  **Exécuter le conteneur** :
    ```bash
    docker run -p 8080:8080 titan-web-server:latest
    ```

### Kubernetes / K3s

1.  **Appliquer les manifestes** :
    ```bash
    kubectl apply -f k8s/
    ```

2.  **Vérifier le déploiement** :
    ```bash
    kubectl get all
    ```

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
