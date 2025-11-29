# TITAN WEB SERVER 🚀

**Titan Web Server** est une solution de serveur web et reverse proxy haute performance, conçue en Go pour allier simplicité, sécurité et extensibilité.

---

## 📋 Prérequis

* **Système d'exploitation** : Linux (recommandé), macOS, ou Windows.
* **Langage** : Go **1.24** ou supérieur.

---

## 🛠️ Installation et Compilation

Titan Web Server est distribué sous forme de code source.

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

3.  **Démarrer le serveur** (utilise `./titan-server.conf` par défaut) :
    ```bash
    ./titan-web-server -config titan-server.conf
    ```

---

## ⚙️ Configuration

Titan Server utilise un format de configuration de type Nginx, avec la prise en charge des répertoires `sites-enabled/` et `conf.d/`.

### Directives Globales

| Directive | Description | Exemple |
| :--- | :--- | :--- |
| `http_port` | Port d'écoute HTTP. | `:8080;` |
| `https_port` | Port d'écoute HTTPS/QUIC. | `:8443;` |
| `log_dir` | Répertoire pour les logs d'accès et d'erreur. | `"/var/log/titan/";` |
| `security` | Bloc pour les options de sécurité globale. | `fips_compliant on;` |
| `cache` | Bloc pour la configuration de la mise en cache (type `memory` ou `disk`). | `type memory;` |

### Bloc `server` et `location`

Un bloc `http` contient des blocs `server` (hôtes virtuels), qui à leur tour contiennent des blocs `location` (routes).

| Type de Route | Directive Clé | Description |
| :--- | :--- | :--- |
| **Statique** | `root` | Sert des fichiers depuis le disque. |
| **Reverse Proxy** | `proxy_pass` / `upstreams` | Redirige le trafic vers un backend (URL ou adresse FastCGI). |
| **PHP (FastCGI)** | `fastcgi_pass` | Exécute des scripts PHP via un gestionnaire PHP-FPM externe. |

---

## 🔒 Sécurité et SSL/TLS

### SSL et HSTS

Le bloc `ssl` permet de configurer le chiffrement pour un serveur.

* **Redirection HTTPS Automatique** : Les requêtes HTTP sont automatiquement redirigées vers HTTPS si SSL est activé pour le site.
* **HSTS** : Prise en charge des directives HTTP Strict Transport Security (`max_age`, `include_subdomains`, `preload`).
* **Conformité FIPS** : Le mode `fips_compliant on` active des contraintes de sécurité élevées (TLS 1.2+ et suites de chiffrement robustes).

### WAF (Web Application Firewall)

Le WAF est basé sur Coraza/ModSecurity pour protéger contre les attaques courantes.

| Directive WAF | Description |
| :--- | :--- |
| `check_sqli` | Protection contre l'injection SQL. |
| `check_xss` | Protection contre les failles XSS (Cross-Site Scripting). |
| `waf_wp` / `waf_joomla` | Règles spécifiques pour les applications WordPress et Joomla. |
| `js_challenge` | Mode de vérification JavaScript pour les attaques. |
| `rate_limit` | Limitation du taux de requêtes par IP. |

---

## ⚡️ Performance et Extensibilité

### Load Balancing

Titan implémente l'équilibrage de charge pour les routes `proxy` en utilisant différents algorithmes :

* **`round_robin`** (défaut)
* **`least_conn`**
* **`ip_hash`**

Les contrôles de santé (`health_check`) de type `web` (HTTP) et `tcp` sont supportés.

### Modules JavaScript

Il est possible d'étendre les fonctionnalités du serveur en injectant des modules JavaScript qui peuvent inspecter et modifier les requêtes et les réponses HTTP.

* **Directive** : 
    ```nginx
    js_module /chemin/vers/votre/module.js;
    ```
* La fonction exportée `main(request, response)` est exécutée lors du traitement de la requête.

### Mise en Cache et GZIP

* **Mise en Cache** : Le cache en mémoire (`memory`) ou sur disque (`disk`) peut être configuré globalement.
    ```nginx
    cache {
        type memory;
        # path "/var/cache/titan/"; # Requis si le type est 'disk'
    }
    ```
* **Compression GZIP** : Activée par la directive `gzip on;` dans le bloc `server`.

---

## 🐳 Déploiement Docker

Le serveur peut être conteneurisé facilement.

1.  **Construire l'image** :
    ```bash
    docker build -t titan-web-server:latest .
    ```

2.  **Exécuter le conteneur** :
    ```bash
    docker run -p 8080:8080 titan-web-server:latest
    ```

---
