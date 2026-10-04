package app

import (
	"fmt"

	"skills/api/prospectsapi"
	"skills/services/websearch/httpfetch"
	"skills/services/websearch/searxng"
	prospectresearch "skills/skills/prospect-research"
)

const prospectResearchName = prospectresearch.Name

// prospectResearch construit le skill prospect-research avec la route de
// lecture de son résultat.
//
// Recherche web : instance SearXNG (service searxng de compose.yaml).
// Lecture des pages : client HTTP, limité aux adresses publiques.
//
// Configuration :
//
//	SEARXNG_URL  URL de l'instance SearXNG (obligatoire)
func prospectResearch(env Env) (module, error) {
	raw := env.Getenv("SEARXNG_URL")
	if raw == "" {
		return module{}, fmt.Errorf("SEARXNG_URL : %w", errMissing)
	}
	engine, err := searxng.New(raw, nil)
	if err != nil {
		return module{}, fmt.Errorf("SEARXNG_URL : %w", err)
	}
	skill, err := prospectresearch.New(prospectresearch.Config{Search: engine, Fetcher: httpfetch.New(httpfetch.Config{})})
	if err != nil {
		return module{}, err
	}
	return module{skill: skill, api: prospectsapi.Options()}, nil
}
