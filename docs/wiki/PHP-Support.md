Titan Web Server supporte nativement l'exécution de scripts PHP via le protocole FastCGI, permettant de servir des applications comme WordPress, Laravel, ou Symfony.

## Architecture

Titan Server n'intègre pas le moteur PHP lui-même, mais agit comme un proxy FastCGI vers un gestionnaire de processus PHP externe, typiquement **PHP-FPM**.

1.  Titan reçoit la requête HTTP (ex: `/index.php`).
2.  Titan transmet la requête à PHP-FPM via TCP ou Socket Unix.
3.  PHP-FPM exécute le script et renvoie la réponse à Titan.
4.  Titan renvoie la réponse au client.

## Configuration de PHP-FPM

Assurez-vous que PHP-FPM est installé et en cours d'exécution.

### Installation (Debian/Ubuntu)
```bash
sudo apt update
sudo apt install php-fpm
```

### Vérification de l'écoute
Vérifiez la configuration de votre pool PHP (souvent `/etc/php/8.x/fpm/pool.d/www.conf`).
Il peut écouter sur un socket Unix ou un port TCP.

*   **Socket Unix** (Défaut) : `listen = /run/php/php8.2-fpm.sock`
*   **TCP** : `listen = 127.0.0.1:9000`

## Configuration de Titan Server

Dans votre fichier `titan-server.conf` (ou via l'interface admin), configurez un bloc `location` avec la directive `fastcgi_pass`.

### Exemple Complet

```nginx
server {
    server_name mon-site-php.local;

    location / {
        # Chemin racine où se trouvent vos fichiers PHP
        root "/var/www/html";

        # Adresse de PHP-FPM
        # Option 1 : Via TCP
        fastcgi_pass 127.0.0.1:9000;

        # Option 2 : Via Socket Unix (syntaxe unix:...)
        # fastcgi_pass unix:/run/php/php8.2-fpm.sock;
    }
}
```

## Fonctionnement Technique

Le gestionnaire PHP de Titan (`NewHybridHandler`) imite le comportement `try_files` de NGINX :

1.  Il vérifie d'abord si le fichier demandé existe physiquement sur le disque (basé sur `root`).
2.  Si le fichier n'existe pas, il renvoie une erreur 404 (évitant de passer des requêtes invalides à PHP-FPM).
3.  Si la requête pointe vers un dossier, il ajoute automatiquement `/index.php`.
4.  Il définit les variables d'environnement FastCGI nécessaires (`SCRIPT_FILENAME`, `SCRIPT_NAME`, etc.).
