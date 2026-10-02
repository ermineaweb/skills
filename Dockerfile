# Étape 1 : compilation de l'API.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# Étape 2 : image d'exécution minimale. La base des fuseaux horaires est
# embarquée dans les binaires (time/tzdata) ; alpine fournit les certificats
# TLS nécessaires pour joindre une API de modèle en HTTPS.
FROM alpine:3.22
RUN adduser -D -u 10001 app
COPY --from=build /out/ /usr/local/bin/
USER app
EXPOSE 8080
ENTRYPOINT ["api"]
