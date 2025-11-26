# Documentation des Modules JavaScript

Titan Web Server prend en charge les modules JavaScript externes pour étendre ses fonctionnalités. Ces modules peuvent être utilisés pour inspecter et modifier les requêtes et les réponses HTTP.

## Création d'un Module

Un module est un simple fichier JavaScript qui exporte une fonction `main`. Cette fonction prend deux arguments : `request` et `response`.

```javascript
// exemple-module.js
function main(request, response) {
    // Ajouter un en-tête à la réponse
    response.headers.Set("X-Powered-By", "Titan Web Server");
}
```

### L'objet `request`

L'objet `request` contient des informations sur la requête HTTP entrante.

*   `request.method` (string) : La méthode HTTP de la requête (par exemple, "GET", "POST").
*   `request.url` (string) : L'URL complète de la requête.
*   `request.headers` (object) : Un objet contenant les en-têtes de la requête.

### L'objet `response`

L'objet `response` peut être utilisé pour modifier la réponse HTTP qui sera envoyée au client.

*   `response.headers` (object) : Un objet contenant les en-têtes de la réponse. Vous pouvez utiliser la méthode `Set` pour ajouter ou modifier un en-tête.

## Activation d'un Module

Pour activer un module, utilisez la directive `js_module` dans votre fichier de configuration `titan-server.conf`. Vous pouvez l'utiliser au niveau du `server` ou du `location`.

### Niveau `server`

Pour activer un module pour toutes les routes d'un site, ajoutez la directive `js_module` au bloc `server`.

```nginx
server {
    server_name exemple.com;
    js_module /chemin/vers/votre/module.js;

    location / {
        proxy_pass http://backend;
    }
}
```

### Niveau `location`

Pour activer un module pour une route spécifique, ajoutez la directive `js_module` au bloc `location`.

```nginx
server {
    server_name exemple.com;

    location /api {
        js_module /chemin/vers/votre/api-module.js;
        proxy_pass http://api-backend;
    }
}
```
