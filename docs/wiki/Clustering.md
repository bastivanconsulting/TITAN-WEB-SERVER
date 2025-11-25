Une des fonctionnalités les plus puissantes de Titan Web Server est sa capacité à former un **cluster distribué** en utilisant l'algorithme de consensus **Raft**.

## Pourquoi le Clustering ?

*   **Haute Disponibilité** : Si un nœud tombe, les autres continuent de servir.
*   **Configuration Unifiée** : Modifiez la configuration sur un nœud (Leader), et elle est instantanément répliquée sur tous les autres nœuds (Followers).
*   **Persistence** : Les changements sont stockés de manière persistante dans le journal Raft.

## Architecture Raft

*   **Leader** : Le nœud responsable de recevoir toutes les modifications de configuration. Il les réplique aux Followers.
*   **Follower** : Reçoit les mises à jour du Leader. Si le Leader échoue, les Followers élisent un nouveau Leader.

## Mise en Place d'un Cluster

### 1. Démarrer le premier nœud (Bootstrap)

Le premier nœud doit initialiser le cluster.

```bash
./titan-web-server \
    -node-id node1 \
    -raft-addr 127.0.0.1:9000 \
    -raft-dir ./raft-data/node1 \
    -bootstrap
```

*   Ce nœud devient automatiquement le **Leader**.

### 2. Ajouter un second nœud

Démarrez le second nœud **sans** le flag `-bootstrap`.

```bash
./titan-web-server \
    -node-id node2 \
    -raft-addr 127.0.0.1:9001 \
    -raft-dir ./raft-data/node2
```

À ce stade, le `node2` tourne mais ne fait pas partie du cluster. Il faut demander au Leader (`node1`) de l'accepter.

### 3. Rejoindre le Cluster (Join)

Il existe trois méthodes pour faire rejoindre `node2` au cluster géré par `node1`.

#### Méthode A : Via l'interface Admin
1.  Connectez-vous à l'admin du **Leader** (`http://127.0.0.1:8080/admin`).
2.  Allez dans l'onglet **Cluster**.
3.  Dans la section "Ajouter un nœud", entrez :
    *   ID du nœud : `node2`
    *   Adresse Raft : `127.0.0.1:9001`
4.  Cliquez sur "Ajouter".

#### Méthode B : Via l'API REST
Envoyez une requête POST au **Leader** :

```bash
curl -X POST http://127.0.0.1:8080/api/cluster/join \
     -H "X-API-Key: votre-cle" \
     -d '{"id": "node2", "addr": "127.0.0.1:9001"}'
```

#### Méthode C : Via CLI au démarrage (Auto-Join)
Démarrez le `node2` avec le flag `-join` pointant vers l'adresse HTTP (API) du Leader ou son adresse Raft (selon implémentation, vérifiez les logs).

```bash
./titan-web-server \
    -node-id node2 \
    ... \
    -join 127.0.0.1:9000
```

## Fonctionnement de la Configuration en Cluster

Dans un cluster Titan :
*   Le fichier `titan-server.conf` sur le disque sert de **snapshot** initial.
*   Une fois le cluster formé, la "Vérité" est la configuration en mémoire gérée par Raft.
*   Lorsque vous modifiez la configuration via l'API ou l'Admin sur le Leader, le changement est appliqué à tous les nœuds.
*   Régulièrement, Raft prend un "Snapshot" de l'état et le sauvegarde sur disque pour accélérer le redémarrage.
