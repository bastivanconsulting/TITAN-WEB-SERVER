# Changelog

## v1.25.1 (2024-07-16)

### NOUVEAUTÉS

*   **Prise en charge des modules JavaScript externes** : Il est désormais possible d'étendre les fonctionnalités du serveur avec des modules JavaScript.
*   **Structure de configuration de type Nginx** : La configuration est maintenant plus familière pour les utilisateurs de Nginx, avec les répertoires `sites-enabled` et `conf.d`.

### CHANGEMENTS

*   **Abandon de l'interface web** : L'interface d'administration web a été complètement supprimée.
*   **Abandon du format JSON** : Le format de configuration JSON a été complètement abandonné au profit d'un format de type Nginx.
*   **Abandon du rechargement à chaud** : Le rechargement à chaud de la configuration a été supprimé. Le serveur doit être redémarré pour que les modifications de la configuration prennent effet.
*   **Abandon du clustering** : La fonctionnalité de clustering a été supprimée.

### CORRECTIONS

*   Aucune correction de bogue dans cette version.
