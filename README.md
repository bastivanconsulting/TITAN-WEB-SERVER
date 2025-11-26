# TITAN WEB SERVER

[![Go CI Status](https://github.com/bastivanconsulting/WebServer/actions/workflows/go.yml/badge.svg?branch=main)](https://github.com/bastivanconsulting/WebServer/actions/workflows/go.yml)

**Titan Web Server** est une solution de serveur web et reverse proxy haute performance, nativement distribuée et sécurisée. Conçu pour l'élasticité et la simplicité, il supporte HTTP/3, la gestion automatique SSL/TLS, et le clustering via Raft.

---

## 📋 Prérequis

*   **Système d'exploitation** : Linux (recommandé pour PAM/Production), macOS, ou Windows.
*   **Langage** : Go **1.24** ou supérieur.
    *   [Télécharger Go](https://go.dev/dl/)

---

## 🛠️ Installation et Compilation

Titan Server est distribué sous forme de code source. Vous devez le compiler pour votre architecture.

1.  **Cloner le dépôt** (ou extraire les sources) :
    ```bash
    git clone https://github.com/bastivanconsulting/WebServer.git
    cd WebServer
    ```

2.  **Compiler le binaire** :
    ```bash
    go mod tidy
    go build -o titan-web-server cmd/server/main.go
    ```

    Cela générera un exécutable `titan-web-server` à la racine.
    *(Note : Pour activer le support PAM sur Linux, assurez-vous d'avoir `libpam0g-dev` installé et CGO activé.)*

---

## ⚙️ Configuration (Format .conf)

Titan Server utilise un format de configuration standardisé **.conf** (syntaxe compatible NGINX).

### Structure des Fichiers
*   **Fichier Principal** : `titan-server.conf` (Doit être passé en argument au démarrage).
*   **Inclusions** : Vous pouvez inclure des configurations de sites via la directive `conf_dir`.

### Exemple de Configuration (`titan-server.conf`)

Créez un fichier `titan-server.conf` avec le contenu suivant :

```nginx
# Ports d'écoute
http_port :8080;
https_port :8443;

# Sécurité et Administration
admin_api_key "votre-clé-secrète";

# Authentification Admin : "admin" (simple) ou "pam" (système)
admin_user "admin";
admin_pass "admin123";
admin_allowlist 127.0.0.1 ::1;

# Logs et Configuration externe
log_dir "/var/log/titan/";
conf_dir "conf.d/";

# Sécurité Globale
security {
    fips_compliant on;
}

# Définition des Services HTTP
http {
    # Site statique avec SSL Let's Encrypt
    server {
        server_name www.example.com;
        ssl {
            enabled on;
            provider letsencrypt;
            email "admin@example.com";
        }
        location / {
            root /var/www/html;
        }
    }
}
```

### Configuration d'un Site PHP

Pour servir une application PHP, Titan délègue l'exécution à un processus **PHP-FPM** externe via FastCGI.

**Exemple de bloc `server` pour PHP :**
```nginx
http {
    # ... autres serveurs ...

    server {
        server_name my-php-app.local;

        location / {
            # Type de handler
            type php;

            # Chemin racine des fichiers (Document Root)
            file_root "/var/www/my-php-app/public";

            # Adresse du processus PHP-FPM
            # Peut être une adresse TCP ou un socket Unix
            proxy_target "127.0.0.1:9000";
            # proxy_target "unix:/var/run/php/php8.1-fpm.sock";
        }
    }
}
```

**Directives importantes pour PHP :**
*   `type php;` : Indique à Titan de traiter les requêtes avec le handler PHP.
*   `file_root "/path/to/your/app";` : Définit le `DOCUMENT_ROOT` de votre application. Titan cherchera `index.php` dans ce répertoire pour les requêtes de type `/`.
*   `proxy_target "address";` : Spécifie comment se connecter à PHP-FPM. C'est l'équivalent de `fastcgi_pass` dans Nginx.

---

## 🖥️ Interface d'Administration

Titan Web Server intègre une **interface web complète** pour gérer votre instance et votre cluster.

### Accès
*   **URL** : `http://votre-serveur:8080/admin`
*   **Restriction IP** : L'accès est limité par la directive `admin_allowlist`.

### Fonctionnalités
1.  **Tableau de Bord** : Vue d'ensemble des sites et du statut.
2.  **Gestion des Sites** :
    *   Création assistée de sites (Proxy, Statique, PHP).
    *   Suppression de sites à chaud.
3.  **Gestion SSL/TLS** :
    *   Activation/Désactivation de SSL par site.
    *   **Upload** de certificats manuels (.crt/.key) directement depuis le navigateur.
    *   Activation de **Let's Encrypt** en un clic.
4.  **Cluster** :
    *   Visualisation de l'état Raft (Leader/Follower).
    *   Formulaire pour rejoindre un cluster existant.
5.  **Éditeur Brut** : Modification directe du fichier de configuration pour les utilisateurs avancés.

### Authentification
L'interface supporte deux modes d'authentification (configurés via `admin_user`) :
*   **Simple** : Utilise `admin_user` et `admin_pass` définis dans le fichier de config.
*   **PAM (Linux)** : Si `admin_user "pam";` est défini, l'authentification se fait via les comptes utilisateurs du système (service PAM `login`).

---

## 🌐 Mode Cluster (Distribué)

Titan Server intègre un moteur de consensus **Raft** pour la synchronisation automatique des configurations entre plusieurs nœuds.

### Démarrer le Premier Nœud (Leader/Bootstrap)
```bash
./titan-web-server \
  -config titan-server.conf \
  -node-id node1 \
  -raft-addr 127.0.0.1:9000 \
  -raft-dir ./data/node1 \
  -bootstrap
```

### Ajouter des Nœuds (Workers)
1.  Démarrer le nœud (sans bootstrap) :
    ```bash
    ./titan-web-server -config titan-server.conf -node-id node2 -raft-addr 127.0.0.1:9001 -raft-dir ./data/node2
    ```
2.  **Via l'Interface Admin** : Allez dans l'onglet "Cluster" et entrez l'ID et l'adresse du nouveau nœud.
3.  **Ou via API** :
    ```bash
    curl -X POST http://127.0.0.1:8080/api/cluster/join \
         -H "X-API-Key: votre-clé-secrète" \
         -d '{"id": "node2", "addr": "127.0.0.1:9001"}'
    ```

---

## 🛡️ Fonctionnalités de Sécurité

*   **SSL Automatique** : Intégration Let's Encrypt native (`provider letsencrypt`).
*   **HSTS** : Strict Transport Security configurable par site.
*   **OCSP Stapling** : Activé par défaut.
*   **Audit Logs** : Logs d'accès et d'erreurs compatibles ISO 27001.

### Module WAF (Web Application Firewall)

Titan intègre un WAF pour protéger vos applications contre les attaques courantes (OWASP Top 10). Le WAF s'active par site et inspecte le trafic entrant.

**Exemple de configuration WAF pour un site :**
```nginx
server {
    server_name www.votre-site.com;

    # ... autres directives ...

    waf {
        enabled on;
        rate_limit 100; # Req/min par IP
        deny_countries CN RU IR;
        deny_list 1.2.3.4 5.6.7.8;
        under_attack_mode off;
    }
}
```

**Directives de configuration :**
*   `enabled [on|off];` : Active ou désactive le WAF pour ce site.
*   `rate_limit [nombre];` : Limite le nombre de requêtes par minute pour une même adresse IP.
*   `allow_list [ip1] [ip2] ...;` : Liste blanche d'IPs qui contournent le WAF.
*   `deny_list [ip1] [ip2] ...;` : Liste noire d'IPs à bloquer.
*   `deny_countries [code1] [code2] ...;` : Bloque le trafic provenant des pays spécifiés (codes ISO 3166-1 alpha-2).
*   `under_attack_mode [on|off];` : Active un mode de sécurité élevé qui bloque temporairement tout le trafic.

**Configuration Globale Requise pour GeoIP :**
Pour utiliser `deny_countries`, vous devez spécifier le chemin vers une base de données GeoIP (format MMDB) dans votre configuration globale :
```nginx
geoip_db_path "/path/to/GeoLite2-Country.mmdb";
```

**Journalisation (Logging) :**
Les requêtes bloquées par le WAF sont enregistrées dans un fichier de log dédié : `/var/log/titan/votredomaine-waf.log`.

---

## 🔍 Dépannage

*   **Erreur de Parsing** : Vérifiez que vos directives se terminent par un point-virgule `;`.
*   **Cluster** : Assurez-vous que les ports Raft (TCP) sont ouverts entre les nœuds.
*   **Droits** : Le processus doit avoir les droits d'écriture dans `log_dir`, `raft-dir` et sur `titan-server.conf`.
