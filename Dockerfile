# Étape 1: Compilation
FROM golang:1.24-alpine AS builder

# Définir le répertoire de travail
WORKDIR /app

# Copier les fichiers go.mod et go.sum et télécharger les dépendances
COPY go.mod go.sum ./
RUN go mod download

# Copier le reste du code source
COPY . .

# Compiler l'application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o titan-web-server cmd/server/main.go

# Étape 2: Image finale
FROM alpine:latest

# Installer les certificats CA
RUN apk --no-cache add ca-certificates

# Définir le répertoire de travail
WORKDIR /app

# Copier le binaire depuis l'étape de compilation
COPY --from=builder /app/titan-web-server .

# Copier le fichier de configuration par défaut
COPY titan-server.conf .

# Copier le fichier de version
COPY VERSION .

# Exposer le port HTTP par défaut
EXPOSE 8080

# Commande pour démarrer le serveur
CMD ["./titan-web-server"]
