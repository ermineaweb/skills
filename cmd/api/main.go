// Commande api : sert l'API JSON du skill prise-de-rendez-vous, avec un
// modèle compatible OpenAI et le calendrier mock. L'interface est servie par
// Caddy (voir ui/Caddyfile), qui relaie /api/* vers ce service.
//
// Variables d'environnement : OPENAI_MODEL (obligatoire), OPENAI_API_KEY,
// OPENAI_BASE_URL (ex: http://localhost:11434/v1 pour Ollama),
// OPENAI_EXTRA_BODY, OPENAI_TIMEOUT (voir model/openai.ConfigFromEnv).
// Gemini : OPENAI_BASE_URL=https://generativelanguage.googleapis.com/v1beta/openai
// et OPENAI_API_KEY=<clé Google AI Studio>.
//
//	go run ./cmd/api -addr :8080 -calendar-tz Europe/Paris
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"skills/agent"
	"skills/api"
	"skills/model/openai"
	"skills/services/calendar"
	priserdv "skills/skills/prise-de-rendez-vous"
)

func main() {
	addr := flag.String("addr", ":8080", "adresse d'écoute")
	calendarTZ := flag.String("calendar-tz", "", "fuseau du calendrier de démonstration (obligatoire), ex: Europe/Paris")
	autoBooking := flag.Bool("auto-booking", false, "politique produit : réservation directe d'un créneau unique")
	warmup := flag.Bool("warmup", true, "préchauffe le modèle au démarrage (utile en local, inutile avec une API en ligne)")
	flag.Parse()
	if err := run(*addr, *calendarTZ, *autoBooking, *warmup); err != nil {
		fmt.Fprintln(os.Stderr, "erreur:", err)
		os.Exit(1)
	}
}

func run(addr, calendarTZ string, autoBooking, warmup bool) error {
	if calendarTZ == "" {
		return errors.New("-calendar-tz est obligatoire (fuseau des horaires d'ouverture du calendrier)")
	}
	loc, err := time.LoadLocation(calendarTZ)
	if err != nil {
		return err
	}
	cfg, err := openai.ConfigFromEnv()
	if err != nil {
		return err
	}
	llm, err := openai.New(cfg)
	if err != nil {
		return fmt.Errorf("%w (définir OPENAI_MODEL)", err)
	}
	cal, err := calendar.NewDemoProvider(loc, time.Now)
	if err != nil {
		return err
	}
	skill, err := priserdv.New(priserdv.Config{Provider: cal, AutoBooking: autoBooking})
	if err != nil {
		return err
	}
	rt, err := agent.New(llm)
	if err != nil {
		return err
	}
	if err := rt.RegisterSkill(skill); err != nil {
		return err
	}

	// Préchauffage : le prompt système et les tools (identiques pour toutes
	// les sessions) sont précalculés par le serveur de modèles, en tâche de
	// fond, pour que la première conversation ne paie pas ce coût. Désactivé
	// pour les API en ligne : aucun gain, et une requête de quota consommée.
	if warmup {
		go func() {
			start := time.Now()
			slog.Info("préchauffage du modèle…")
			if err := rt.Warmup(context.Background(), priserdv.Name); err != nil {
				slog.Warn("préchauffage du modèle échoué", "error", err)
				return
			}
			slog.Info("modèle prêt", "durée", time.Since(start).Round(time.Second).String())
		}()
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           api.New(rt, cal, time.Now, api.WithActivatedSkills(priserdv.Name)).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	slog.Info("API démarrée", "addr", addr)
	return srv.ListenAndServe()
}
