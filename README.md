# TITAN WEB SERVER

[![Go CI Status](https://github.com/bastivanconsulting/WebServer/actions/workflows/go.yml/badge.svg?branch=main)](https://github.com/bastivanconsulting/WebServer/actions/workflows/go.yml)

**Titan Web Server** est une solution de serveur web et reverse proxy haute performance, nativement distribuée et sécurisée. Conçu pour l'élasticité et la simplicité, il supporte HTTP/3, la gestion automatique SSL/TLS, et le clustering via Raft.

---

## 📋 Prérequis

*   **Système d'exploitation** : Linux, macOS, ou Windows.
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

---

## ⚙️ Configuration (Migration .conf)

Titan Server utilise désormais un format de configuration standardisé **.conf** (syntaxe compatible NGINX). Le format JSON n'est plus supporté.

### Structure des Fichiers
*   **Fichier Principal** : `titan-server.conf` (Doit être passé en argument au démarrage).
*   **Inclusions** : Vous pouvez inclure des configurations de sites via la directive `conf_dir`.

### Exemple de Configuration (`titan-server.conf`)

Créez un fichier `titan-server.conf` avec le contenu suivant :

```nginx
# Utilisateur et Processus (Non utilisé pour le moment, réservé pour le futur)
user titan;
worker_processes auto;

# Ports d'écoute
http_port :8080;
https_port :8443;

# Sécurité et Administration
admin_api_key "votre-clé-secrète";
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
    # Site Statique
    server {
        server_name www.example.com;

        ssl {
            enabled on;
            provider letsencrypt;
            email "admin@example.com";
            ocsp_stapling on;
        }

        location / {
            root /var/www/html;
        }
    }

    # Reverse Proxy
    server {
        server_name api.local;

        location / {
            proxy_pass http://127.0.0.1:3000;
        }
    }

    # PHP (FastCGI)
    server {
        server_name app.php;

        location / {
            root /var/www/phpapp;
            fastcgi_pass 127.0.0.1:9000;
        }
    }
}
```

### Directives Principales
*   **http_port / https_port** : Ports d'écoute.
*   **admin_api_key** : Clé pour l'API REST d'administration.
*   **security { fips_compliant on; }** : Active le mode conformité FIPS (TLS 1.2+, Ciphers restreints).
*   **server { ... }** : Définit un site (Virtual Host).
*   **location / { ... }** : Définit une route.
    *   `root` : Racine pour fichiers statiques.
    *   `proxy_pass` : Cible pour Reverse Proxy.
    *   `fastcgi_pass` : Cible pour PHP-FPM.

---

## 🌐 Mode Cluster (Distribué)

Titan Server intègre un moteur de consensus **Raft** pour la synchronisation automatique des configurations entre plusieurs nœuds.

### Démarrer le Premier Nœud (Leader/Bootstrap)
Pour initialiser un nouveau cluster :

```bash
./titan-web-server \
  -config titan-server.conf \
  -node-id node1 \
  -raft-addr 127.0.0.1:9000 \
  -raft-dir ./data/node1 \
  -bootstrap
```

*   `-bootstrap` : Indique que ce nœud doit initialiser le cluster (à utiliser uniquement pour le premier nœud).

### Ajouter des Nœuds (Workers)
Pour ajouter d'autres nœuds au cluster :

1.  Démarrer le second nœud (sans bootstrap) :
    ```bash
    ./titan-web-server \
      -config titan-server.conf \
      -node-id node2 \
      -raft-addr 127.0.0.1:9001 \
      -raft-dir ./data/node2 \
      -join 127.0.0.1:9000
    ```
    *   `-join` : Adresse HTTP d'administration du leader (Non, l'implémentation actuelle demande une action manuelle ou via API, la commande ci-dessus est une simplification pour l'exemple, référez-vous à l'API).

    *Correction : L'option `-join` tente de rejoindre via le protocole Raft ou API selon l'implémentation. Dans la version actuelle, utilisez l'API pour joindre.*

**Méthode Recommandée pour Joindre :**

1.  Démarrer le nœud 2 :
    ```bash
    ./titan-web-server -config titan-server.conf -node-id node2 -raft-addr 127.0.0.1:9001 -raft-dir ./data/node2
    ```

2.  Demander au Leader (Node 1) d'ajouter Node 2 via l'API :
    ```bash
    curl -X POST http://127.0.0.1:8080/api/cluster/join \
         -H "X-API-Key: votre-clé-secrète" \
         -d '{"id": "node2", "addr": "127.0.0.1:9001"}'
    ```

### Synchronisation
Une fois le cluster formé, toute modification de configuration envoyée au Leader (via API ou fichier) est automatiquement répliquée sur tous les nœuds. Le fichier `titan-server.conf` est mis à jour localement sur chaque nœud.

---

## 🛡️ Fonctionnalités de Sécurité

*   **SSL Automatique** : Intégration Let's Encrypt native (`provider letsencrypt`).
*   **HSTS** : Strict Transport Security configurable par site.
*   **OCSP Stapling** : Activé par défaut pour les performances et la confidentialité.
*   **Audit Logs** : Logs d'accès et d'erreurs au format compatible ISO 27001 dans `log_dir`.

---

## 🔍 Dépannage

*   **Erreur de Parsing** : Vérifiez que vos directives se terminent par un point-virgule `;` et que les blocs sont bien fermés par `}`.
*   **Cluster** : Assurez-vous que les ports Raft (TCP) sont ouverts entre les nœuds.
*   **Droits** : Le processus doit avoir les droits d'écriture dans `log_dir`, `raft-dir` et sur le fichier `titan-server.conf`.
