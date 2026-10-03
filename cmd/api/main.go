// Commande api : sert l'API JSON de l'agent, avec un modèle compatible
// OpenAI et les skills choisis par la configuration (voir app.ConfigFromEnv).
// L'interface est servie par Caddy (voir ui/Caddyfile), qui relaie /api/*
// vers ce service.
//
// Variables d'environnement :
//
//	OPENAI_MODEL (obligatoire), OPENAI_API_KEY, OPENAI_BASE_URL,
//	OPENAI_EXTRA_BODY, OPENAI_TIMEOUT   modèle (voir model/openai.ConfigFromEnv)
//	SKILLS          skills enregistrés, séparés par des virgules (défaut : tous)
//	ACTIVE_SKILLS   skills actifs dès l'ouverture d'une session (défaut : aucun)
//	AGENT_MAX_STEPS appels au modèle par message (défaut : celui du runtime)
//	…               configuration propre à chaque skill (voir app/)
//
//	CALENDAR_TZ=Europe/Paris go run ./cmd/api -addr :8080
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"skills/api"
	"skills/app"
	"skills/model/openai"
)

func main() {
	addr := flag.String("addr", ":8080", "adresse d'écoute")
	warmup := flag.Bool("warmup", true, "préchauffe le modèle au démarrage (utile en local, inutile avec une API en ligne)")
	flag.Parse()
	if err := run(*addr, *warmup); err != nil {
		fmt.Fprintln(os.Stderr, "erreur:", err)
		os.Exit(1)
	}
}

func run(addr string, warmup bool) error {
	cfg, err := openai.ConfigFromEnv()
	if err != nil {
		return err
	}
	llm, err := openai.New(cfg)
	if err != nil {
		return fmt.Errorf("%w (définir OPENAI_MODEL)", err)
	}
	appCfg, err := app.ConfigFromEnv()
	if err != nil {
		return err
	}
	a, err := app.New(llm, appCfg)
	if err != nil {
		return err
	}

	// Préchauffage : le prompt système et les tools des skills actifs
	// d'office (identiques pour toutes les sessions) sont précalculés par le
	// serveur de modèles, en tâche de fond, pour que la première conversation
	// ne paie pas ce coût. Désactivé pour les API en ligne : aucun gain, et
	// une requête de quota consommée.
	if warmup {
		go func() {
			start := time.Now()
			slog.Info("préchauffage du modèle…")
			if err := a.Runtime.Warmup(context.Background(), a.Activated...); err != nil {
				slog.Warn("préchauffage du modèle échoué", "error", err)
				return
			}
			slog.Info("modèle prêt", "durée", time.Since(start).Round(time.Second).String())
		}()
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           api.New(a.Runtime, time.Now, a.APIOptions...).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	slog.Info("API démarrée", "addr", addr, "skills", a.Skills, "actifs", a.Activated)
	return srv.ListenAndServe()
}
