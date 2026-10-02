// Commande chat : conversation interactive avec un vrai LLM via une API
// compatible OpenAI (OpenAI, Ollama, vLLM, Mistral…), sur le calendrier mock.
//
// Variables d'environnement :
//
//	OPENAI_MODEL     nom du modèle (obligatoire)
//	OPENAI_API_KEY   clé d'API (facultative pour Ollama)
//	OPENAI_BASE_URL  défaut https://api.openai.com/v1 (Ollama : http://localhost:11434/v1)
//
//	go run ./cmd/chat -tz Europe/Paris [-client-id c-42 -client-name "Camille Durand"]
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"skills/agent"
	"skills/model/openai"
	"skills/services/calendar"
	priserdv "skills/skills/prise-de-rendez-vous"
	"skills/types"
)

func main() {
	tz := flag.String("tz", "", "fuseau horaire IANA de l'utilisateur (obligatoire), ex: Europe/Paris")
	clientID := flag.String("client-id", "", "identifiant du client authentifié (facultatif)")
	clientName := flag.String("client-name", "", "nom du client authentifié (facultatif)")
	autoBooking := flag.Bool("auto-booking", false, "politique produit : réservation directe d'un créneau unique")
	verbose := flag.Bool("v", false, "affiche les appels de tools")
	flag.Parse()
	if err := run(*tz, types.UserContext{ClientID: *clientID, Name: *clientName}, *autoBooking, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, "erreur:", err)
		os.Exit(1)
	}
}

func run(tz string, user types.UserContext, autoBooking, verbose bool) error {
	if tz == "" {
		return errors.New("-tz est obligatoire : le fuseau n'est jamais supposé")
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return err
	}

	// Le seul endroit qui choisit un fournisseur d'IA : le point d'entrée.
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
	session, err := agent.NewSession("chat", tz, user)
	if err != nil {
		return err
	}

	fmt.Println("Calendrier de démonstration : Paul Martin et Marie Dubois, jours ouvrés des 14 prochains jours.")
	fmt.Println("Tapez votre message (Ctrl-D pour quitter).")
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\nvous > ")
		if !in.Scan() {
			fmt.Println()
			return in.Err()
		}
		text := strings.TrimSpace(in.Text())
		if text == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		reply, err := rt.Handle(ctx, session, text)
		cancel()
		if verbose {
			for _, st := range reply.Steps {
				fmt.Printf("  [tool] %s %s → %s\n", st.Tool, st.Arguments, st.Result)
			}
		}
		// Les confirmations affichées par l'application viennent des effets
		// confirmés par le calendrier, pas du texte généré.
		for _, e := range reply.Effects {
			fmt.Printf("  [calendrier] opération confirmée : %s\n", e.Tool)
		}
		if err != nil {
			fmt.Println("agent > Désolé, je rencontre un problème technique. Pouvez-vous réessayer ?")
			fmt.Fprintln(os.Stderr, "  (détail :", err, ")")
			continue
		}
		fmt.Println("agent >", reply.Text)
	}
}
