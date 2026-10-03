package app

import (
	"fmt"

	"skills/api/prospectsapi"
	"skills/services/websearch/websearchtest"
	prospectresearch "skills/skills/prospect-research"
)

const prospectResearchName = prospectresearch.Name

// prospectResearch construit le skill prospect-research avec la route de
// lecture de son résultat.
//
// Aucun moteur de recherche réel n'est encore branché : le skill utilise le
// moteur simulé de websearchtest (entreprises fictives, aucun appel réseau).
//
// Configuration :
//
//	WEBSEARCH_SCENARIO  scénario du moteur simulé (défaut basic), voir
//	                    services/websearch/websearchtest/testdata/prospect-research
func prospectResearch(env Env) (module, error) {
	name := env.Getenv("WEBSEARCH_SCENARIO")
	if name == "" {
		name = "basic"
	}
	sc, err := websearchtest.LoadScenario(websearchtest.Fixtures(), name)
	if err != nil {
		names, _ := websearchtest.ScenarioNames(websearchtest.Fixtures())
		return module{}, fmt.Errorf("WEBSEARCH_SCENARIO : %w (disponibles : %v)", err, names)
	}
	skill, err := prospectresearch.New(prospectresearch.Config{Search: sc.Search, Fetcher: sc.Fetcher})
	if err != nil {
		return module{}, err
	}
	return module{skill: skill, api: prospectsapi.Options()}, nil
}
