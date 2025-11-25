La configuration de Titan Web Server utilise un format inspiré de NGINX, structuré en blocs et directives terminées par des points-virgules `;`.

## Structure Globale

Le fichier de configuration se divise en deux parties principales :
1.  **Directives Globales** : Configuration du serveur lui-même (ports, admin, logs).
2.  **Bloc HTTP** : Définition des sites et services web.

### Directives Globales

Ces directives se placent au sommet du fichier :

| Directive | Description | Exemple |
| :--- | :--- | :--- |
| `http_port` | Port d'écoute HTTP. | `:8080;` |
| `https_port` | Port d'écoute HTTPS/QUIC. | `:8443;` |
| `admin_api_key` | Clé secrète pour l'API d'administration. | `"votre-cle";` |
| `admin_user` | Nom d'utilisateur admin ou "pam" pour l'auth système. | `"admin";` |
| `admin_pass` | Mot de passe admin (si non PAM). | `"secret";` |
| `admin_allowlist` | Liste des IP autorisées à accéder à l'admin. | `127.0.0.1 ::1;` |
| `log_dir` | Répertoire pour les logs d'accès et d'erreur. | `"/var/log/titan/";` |
| `conf_dir` | Répertoire pour inclure des fichiers `.conf` supplémentaires. | `"conf.d/";` |

### Bloc Security

```nginx
security {
    fips_compliant on;
}
```
*   `fips_compliant` : Active le mode de conformité FIPS (on/off).

## Bloc HTTP et Server

Le bloc `http` contient un ou plusieurs blocs `server`. Chaque bloc `server` représente un hôte virtuel (Virtual Host).

```nginx
http {
    server {
        server_name www.example.com;

        # Configuration SSL
        ssl {
            enabled on;
            provider letsencrypt; # ou 'manual'
            email "admin@example.com";

            # Options avancées
            ocsp_stapling on;
            hsts {
                max_age 31536000;
                include_subdomains on;
                preload on;
            }
        }

        # Routes (Locations)
        location / {
            root /var/www/html;
        }
    }
}
```

### Configuration SSL

À l'intérieur d'un bloc `server`, le bloc `ssl` configure le chiffrement.

*   `enabled` : Active ou désactive SSL (`on`/`off`).
*   `provider` :
    *   `letsencrypt` : Génère automatiquement des certificats via Let's Encrypt.
    *   `manual` : Utilise les fichiers spécifiés dans `cert_file` et `key_file`.
*   `email` : Requis pour Let's Encrypt.
*   `cert_file` / `key_file` : Chemins vers le certificat et la clé privée (pour `provider manual`).

### Locations (Routes)

Le bloc `location` définit comment traiter les requêtes pour un chemin donné.

#### 1. Fichiers Statiques
Servir des fichiers depuis le disque.
```nginx
location /static {
    root "./public_html";
}
```

#### 2. Reverse Proxy
Rediriger le trafic vers un autre serveur (backend).
```nginx
location /api {
    proxy_pass http://127.0.0.1:3000;
}
```

#### 3. PHP (FastCGI)
Exécuter des scripts PHP.
```nginx
location / {
    root "/var/www/site/public";
    fastcgi_pass 127.0.0.1:9000; # ou unix:/var/run/php/php-fpm.sock
}
```

**Note** : Pour le support PHP, voir la page [Support PHP](PHP-Support).
