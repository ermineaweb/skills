// Commande chat : conversation interactive avec un vrai LLM via une API
// compatible OpenAI (OpenAI, Ollama, vLLM, Mistral…), avec les skills choisis
// par la configuration (voir app.ConfigFromEnv).
//
// Variables d'environnement :
//
//	OPENAI_MODEL     nom du modèle (obligatoire)
//	OPENAI_API_KEY   clé d'API (facultative pour Ollama)
//	OPENAI_BASE_URL  défaut https://api.openai.com/v1 (Ollama : http://localhost:11434/v1)
//	SKILLS, ACTIVE_SKILLS et la configuration propre à chaque skill (voir app/)
//
//	CALENDAR_TZ=Europe/Paris go run ./cmd/chat -tz Europe/Paris [-client-id c-42 -client-name "Camille Durand"]
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

	"skills/app"
	"skills/model/openai"
	"skills/types"
)

func main() {
	tz := flag.String("tz", "", "fuseau horaire IANA de l'utilisateur (obligatoire), ex: Europe/Paris")
	clientID := flag.String("client-id", "", "identifiant du client authentifié (facultatif)")
	clientName := flag.String("client-name", "", "nom du client authentifié (facultatif)")
	verbose := flag.Bool("v", false, "affiche les appels de tools")
	flag.Parse()
	if err := run(*tz, types.UserContext{ClientID: *clientID, Name: *clientName}, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, "erreur:", err)
		os.Exit(1)
	}
}

func run(tz string, user types.UserContext, verbose bool) error {
	if tz == "" {
		return errors.New("-tz est obligatoire : le fuseau n'est jamais supposé")
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

	appCfg, err := app.ConfigFromEnv()
	if err != nil {
		return err
	}
	a, err := app.New(llm, appCfg)
	if err != nil {
		return err
	}
	session, err := a.NewSession("chat", tz, user)
	if err != nil {
		return err
	}

	fmt.Printf("Skills : %s (actifs d'office : %s).\n", strings.Join(a.Skills, ", "), orNone(a.Activated))
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
		reply, err := a.Runtime.Handle(ctx, session, text)
		cancel()
		if verbose {
			for _, st := range reply.Steps {
				fmt.Printf("  [tool] %s %s → %s\n", st.Tool, st.Arguments, st.Result)
			}
		}
		// Les confirmations affichées par l'application viennent des effets
		// confirmés par les tools, pas du texte généré.
		for _, e := range reply.Effects {
			fmt.Printf("  [effet] opération confirmée : %s\n", e.Tool)
		}
		if err != nil {
			fmt.Println("agent > Désolé, je rencontre un problème technique. Pouvez-vous réessayer ?")
			fmt.Fprintln(os.Stderr, "  (détail :", err, ")")
			continue
		}
		fmt.Println("agent >", reply.Text)
	}
}

func orNone(names []string) string {
	if len(names) == 0 {
		return "aucun"
	}
	return strings.Join(names, ", ")
}
