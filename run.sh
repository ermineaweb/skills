#!/usr/bin/env bash
# Lance toute la stack : interface (Caddy), API (Go) et modèle : API en ligne
# (MODEL_BASE_URL défini dans .env) ou Ollama local (par défaut).
#
#   ./run.sh          construit et démarre la stack, puis affiche l'URL
#   ./run.sh logs     suit les logs
#   ./run.sh stop     arrête la stack (le modèle téléchargé est conservé)
set -euo pipefail

cd "$(dirname "$0")"

die()  { echo "✗ $*" >&2; exit 1; }
info() { echo "→ $*"; }

# --- Docker ---------------------------------------------------------------
command -v docker >/dev/null || die "Docker n'est pas installé."
docker compose version >/dev/null 2>&1 || die "Docker Compose v2 est requis (docker compose)."
if ! docker info >/dev/null 2>&1; then
  # Utilisateur ajouté au groupe docker sans nouvelle session : on relance via sg.
  if getent group docker | cut -d: -f4 | tr ',' '\n' | grep -qx "$(id -un)" \
     && [ -z "${RUN_SH_SG:-}" ]; then
    exec sg docker -c "RUN_SH_SG=1 $(printf '%q ' "$0" "$@")"
  fi
  die "Impossible de joindre Docker (droits sur /var/run/docker.sock ?)."
fi

case "${1:-up}" in
  up) ;;
  logs) exec docker compose logs -f ;;
  # Le profil inclut Ollama pour l'arrêter aussi s'il tourne.
  stop|down) exec docker compose --profile ollama down ;;
  *) die "Usage : $0 [up|logs|stop]" ;;
esac

# --- Configuration (facultative) --------------------------------------------
if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi
model="${MODEL:-qwen3:4b-instruct}"
url="http://localhost:${WEB_PORT:-8080}"

# --- Démarrage --------------------------------------------------------------
if [ -n "${MODEL_BASE_URL:-}" ]; then
  [ -n "${MODEL_API_KEY:-}" ] || die "MODEL_API_KEY est vide dans .env (clé de l'API ${MODEL_BASE_URL})."
  info "Modèle en ligne : ${model} (${MODEL_BASE_URL})."
  # Ollama n'est pas lancé ; on l'arrête s'il reste d'un lancement local.
  docker compose --profile ollama stop ollama >/dev/null 2>&1 || true
else
  export COMPOSE_PROFILES=ollama
  info "Modèle local : ${model}. Au premier lancement, il est téléchargé (plusieurs Go) : cela peut prendre quelques minutes."
fi
info "Construction et démarrage des services…"
docker compose up --build -d

if command -v curl >/dev/null; then
  info "Attente de l'interface…"
  for _ in $(seq 1 60); do
    if curl -sf -o /dev/null "$url/"; then break; fi
    sleep 1
  done
  curl -sf -o /dev/null "$url/" || die "L'interface ne répond pas. Voir : $0 logs"
fi

docker compose ps --format '  {{.Service}}: {{.Status}}'
echo
echo "✓ Stack démarrée : $url"
[ -n "${MODEL_BASE_URL:-}" ] \
  || echo "  Sans GPU, la première réponse peut prendre une à deux minutes (chargement du modèle)."
echo "  Logs : $0 logs    Arrêt : $0 stop"
