Titan Web Server expose une API REST complète pour l'automatisation et la gestion programmatique. Cette API est sécurisée par la clé définie dans `admin_api_key`.

## Authentification

Toutes les requêtes doivent inclure l'en-tête HTTP suivant :
`X-API-Key: <votre-clé-secrète>`

## Endpoints Principaux

Base URL : `http://<host>:<port>/api`

### Statut et Système

#### `GET /api/status`
Retourne l'état de santé du serveur.
*   **Réponse 200 OK** : `{"status": "ok", "version": "..."}`

### Cluster

#### `GET /api/cluster/status`
Retourne les détails du nœud Raft actuel (Leader/Follower, ID, Peers).

#### `POST /api/cluster/join`
Demande au Leader d'ajouter un nouveau nœud au cluster.
*   **Body (JSON)** :
    ```json
    {
      "id": "node2",
      "addr": "10.0.0.2:9000"
    }
    ```

### Configuration

#### `GET /api/config`
Récupère la configuration actuelle du serveur au format JSON.

#### `POST /api/config`
Met à jour la configuration du serveur. Cela déclenchera un rechargement à chaud sur tous les nœuds du cluster.
*   **Body** : Le contenu du fichier `.conf` ou la structure JSON équivalente.

#### `POST /api/reload`
Force le rechargement de la configuration depuis le disque (utile hors mode cluster).

## Exemple d'Utilisation (cURL)

Vérifier le statut du cluster :

```bash
curl -s http://localhost:8080/api/cluster/status \
  -H "X-API-Key: change-this-secret-key" | jq
```
