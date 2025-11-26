# Changelog

Toutes les modifications notables de ce projet seront documentées dans ce fichier.

Le format est basé sur [Keep a Changelog](https://keepachangelog.com/fr/1.0.0/),
et ce projet adhère à la [Gestion Sémantique de Version](https://semver.org/lang/fr/).

## [1.25.0] - 2025-11-25

### ✨ Nouveautés

- **Configuration du WAF par `location`** : Il est maintenant possible de définir des règles de pare-feu applicatif web (WAF) spécifiques à une `location`. Cela permet un contrôle de sécurité beaucoup plus granulaire. Par exemple, vous pouvez appliquer des limites de taux plus strictes sur une page de connexion (`/login`) tout en désactivant le WAF pour les ressources statiques (`/static`). Les règles définies dans une `location` surchargent celles du `server`.

- **Challenge JavaScript** : Une nouvelle protection contre les bots a été ajoutée. En activant le `js_challenge` dans un bloc `waf`, le serveur présentera aux visiteurs une page de vérification qui s'assure qu'ils utilisent un navigateur capable d'exécuter du JavaScript. C'est une méthode efficace pour filtrer les bots simples et réduire le trafic automatisé.

### 📚 Documentation

- Le `README.md` a été mis à jour pour inclure des explications et des exemples pour les nouvelles fonctionnalités du WAF.
- Le fichier d'exemple `titan-server.conf` a été enrichi pour montrer comment utiliser le WAF par `location` et le challenge JavaScript.
