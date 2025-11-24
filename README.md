# TITAN WEB SERVER

Bienvenue dans **Titan Web Server**, une solution ultra-performante et sécurisée pour héberger vos sites web et rediriger le trafic (Reverse Proxy). Ce serveur est conçu pour être **simple à utiliser**, même si vous n'avez aucune expérience en administration système.

Ce guide vous accompagnera étape par étape pour configurer et lancer votre serveur.

## Fonctionnalités Clés

*   **Hébergement Web** : Affichez vos fichiers HTML, CSS, et images.
*   **Reverse Proxy** : Redirigez les visiteurs vers d'autres applications (ex: Node.js, Python).
*   **Sécurité Maximale** :
    *   **FIPS Compliance** : Normes de sécurité gouvernementales.
    *   **HSTS & SSL** : Protection automatique des connexions (HTTPS).
    *   **Logs ISO 27001** : Journalisation détaillée pour l'audit et la sécurité.
*   **Performance** : Supporte HTTP/3 (la version la plus rapide du web).
*   **Gestion Facile** : Une interface d'administration simple et un rechargement automatique de la configuration.

---

## Installation

### Prérequis

Pour utiliser Titan Web Server, vous avez juste besoin d'avoir **Go** installé sur votre machine (version 1.24 ou plus).
Si vous ne l'avez pas, téléchargez-le ici : [https://go.dev/dl/](https://go.dev/dl/)

### Compiler le Programme

Ouvrez votre terminal (ou invite de commande) dans le dossier du projet et tapez :

```bash
go build -o titan-web-server cmd/server/main.go
```

Cela va créer un fichier exécutable nommé `titan-web-server`.

---

## Guide de Démarrage Rapide

### 1. Préparer les dossiers

Créez un dossier pour vos logs (journaux d'activité). Par défaut, Titan utilise `/var/log/titan/`.
Si vous êtes sous Linux/Mac :
```bash
sudo mkdir -p /var/log/titan/
sudo chmod 777 /var/log/titan/
```

### 2. Créer la Configuration (`config.json`)

Le fichier `config.json` est le cœur de votre serveur. Créez ce fichier à côté de l'exécutable `titan-web-server`.

Voici un exemple complet et commenté pour vous aider à comprendre chaque ligne. Copiez-collez ceci dans votre fichier `config.json` :

```json
{
    "http_port": ":8080",
    "https_port": ":8443",
    "admin_api_key": "votre-clé-secrète-ici",
    "admin_user": "admin",
    "admin_pass": "mot-de-passe-admin",
    "admin_allowlist": [
        "127.0.0.1",
        "::1"
    ],
    "log_dir": "/var/log/titan/",
    "security": {
        "fips_compliant": true
    },
    "sites": [
        {
            "domain": "localhost",
            "ssl": {
                "enabled": false
            },
            "routes": [
                {
                    "path": "/",
                    "type": "static",
                    "file_root": "./mon-site-web"
                },
                {
                    "path": "/app",
                    "type": "proxy",
                    "proxy_target": "http://localhost:3000"
                }
            ]
        }
    ]
}
```

### 3. Lancer le Serveur

Dans votre terminal, lancez la commande suivante :

```bash
./titan-web-server -config config.json
```

Si tout va bien, vous verrez des messages indiquant que le serveur a démarré.

---

## Comprendre le Fichier de Configuration (`config.json`)

Voici une explication simple de chaque option :

### Paramètres Généraux

*   **`http_port`** : Le port pour le trafic normal (ex: `":80"` pour le web standard, ou `":8080"` pour tester).
*   **`https_port`** : Le port pour le trafic sécurisé (ex: `":443"`). Laissez vide si vous n'utilisez pas SSL.
*   **`log_dir`** : L'endroit où les fichiers de logs seront enregistrés.
*   **`admin_...`** : Ces champs protègent l'interface d'administration (`http://localhost:8080/admin`). Changez le mot de passe !

### Sécurité (`security`)

*   **`fips_compliant`** : Mettez `true` (vrai) pour activer le mode "Haute Sécurité". Cela force le serveur à utiliser uniquement des chiffrements très robustes.

### Sites (`sites`)

C'est ici que vous définissez vos noms de domaine.

*   **`domain`** : Votre nom de domaine (ex: `monsite.com` ou `localhost`).
*   **`ssl`** : Configuration du cadenas vert (HTTPS).
    *   `enabled` : `true` pour activer.
    *   `provider` : Mettez `"manual"` pour fournir vos propres certificats.
    *   `cert_file` / `key_file` : Chemins vers vos fichiers de certificat.
    *   `hsts` : Options avancées pour forcer le navigateur à toujours utiliser HTTPS.
    *   `ocsp_stapling` : Améliore la vitesse et la confidentialité de la vérification SSL.

### Routes (`routes`)

Pour chaque site, vous pouvez avoir plusieurs chemins (routes).

*   **Type `static`** : Pour héberger des fichiers simples (HTML, images).
    *   `path` : L'URL (ex: `/` pour la racine).
    *   `file_root` : Le dossier sur votre ordinateur qui contient les fichiers (ex: `./mon-site-web`).

*   **Type `proxy`** : Pour rediriger vers une autre application.
    *   `proxy_target` : L'adresse de l'autre application (ex: `http://localhost:3000`).
    *   **Compatible WebSocket** : Titan gère automatiquement les connexions WebSocket pour les applications en temps réel.

---

## Logs et Surveillance (ISO 27001)

Pour respecter les normes de sécurité, Titan crée deux fichiers par site dans votre dossier `log_dir` :

1.  **`domaine-access.log`** : Liste toutes les visites (qui, quand, quelle page, code de réponse).
2.  **`domaine-error.log`** : Liste toutes les erreurs techniques.

Vous pouvez ouvrir ces fichiers avec n'importe quel éditeur de texte pour voir ce qui se passe sur votre serveur.

---

## Dépannage (Problèmes fréquents)

**Le serveur ne démarre pas ?**
*   Vérifiez que le format JSON est correct (pas de virgule manquante).
*   Vérifiez que les ports (80, 443, 8080) ne sont pas déjà utilisés par un autre programme (comme Apache ou Nginx).

**Je modifie `config.json` mais rien ne change ?**
*   Titan recharge la configuration automatiquement ! Regardez la console (terminal) où le serveur tourne. Si vous avez fait une erreur dans le JSON, il vous affichera un message d'erreur et gardera l'ancienne configuration pour ne pas planter.

**Erreur "Permission denied" pour les logs ?**
*   Assurez-vous que le dossier `/var/log/titan/` existe et que votre utilisateur a le droit d'écrire dedans. Sinon, changez `log_dir` pour un dossier où vous avez les droits (ex: `./logs/`).

**Le proxy ne marche pas avec les WebSockets ?**
*   Titan supporte les WebSockets nativement. Assurez-vous que votre application backend (cible du proxy) fonctionne correctement.

---

## Licence

Ce projet est sous licence MIT. Vous êtes libre de l'utiliser et de le modifier.
