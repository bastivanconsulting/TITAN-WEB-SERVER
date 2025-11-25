# SECURITY.md - Politique de Sécurité du Titan Web Server

## Politique de Sécurité

La sécurité est une priorité pour le **Titan Web Server**. Nous nous engageons à corriger rapidement toute vulnérabilité signalée.

---

## Versions Supportées

Actuellement, seule la dernière version du Titan Web Server est activement maintenue et reçoit des correctifs de sécurité. Nous recommandons de toujours compiler et déployer à partir de la dernière source, qui utilise Go 1.24.x.

| Version | Statut |
| :--- | :--- |
| **Go 1.24.x** (Source actuelle) | :white_check_mark: Supportée |
| Versions antérieures | :x: Non supportées |

---

## Signalement d'une Vulnérabilité

Si vous découvrez une vulnérabilité, veuillez la signaler de manière responsable et **NE PAS** la divulguer publiquement.

**Veuillez envoyer un e-mail à :**
`contact@bastivanconsulting.com` 

Nous vous accuserons réception dans les **7 jours ouvrables** et vous tiendrons informé de l'état du correctif.

---

## Fonctionnalités de Sécurité Clés

Le Titan Web Server intègre les mécanismes de sécurité suivants :

* **Conformité FIPS 140-2/3**: Active le mode haute sécurité, forçant l'utilisation de **TLS 1.2+** et des suites de chiffrements robustes.
* **API d'Administration Protégée**: L'accès est sécurisé par une **Clé API** (`X-API-Key`) et une **Liste Blanche d'IP** (`admin_allowlist`).
* **OCSP Stapling**: Gère et utilise les agrafes OCSP pour accélérer la vérification des certificats et améliorer la confidentialité.
* **Logs ISO 27001**: Journalisation détaillée des accès et des erreurs pour l'audit et la surveillance.
