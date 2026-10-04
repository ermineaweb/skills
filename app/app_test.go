package app_test

import (
	"strings"
	"testing"

	"skills/app"
	"skills/model/scripted"
	"skills/types"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// valid renvoie une configuration complète, modifiée par overrides ("" :
// variable absente).
func valid(overrides map[string]string) func(string) string {
	vars := map[string]string{
		"CALENDAR_TZ":          "Europe/Paris",
		"SEARXNG_URL":          "http://searxng:8080",
		"TTS_PROVIDER":         "kokoro",
		"TTS_BASE_URL":         "http://kokoro:8880",
		"TTS_VOICES":           "fr-FR=ff_siwis;en-US=af_heart,am_adam",
		"TTS_DEFAULT_LANGUAGE": "fr-FR",
	}
	for k, v := range overrides {
		vars[k] = v
	}
	return env(vars)
}

func TestDefaultRegistersAllSkills(t *testing.T) {
	a, err := app.New(scripted.New(), app.Config{Getenv: valid(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(a.Skills, ","), strings.Join(app.Available(), ","); got != want {
		t.Fatalf("skills = %s, attendu %s", got, want)
	}
	if len(a.Activated) != 0 {
		t.Fatalf("aucun skill ne doit être actif par défaut : %v", a.Activated)
	}
}

func TestActivatedSkills(t *testing.T) {
	a, err := app.New(scripted.New(), app.Config{
		Skills:    []string{"prise-de-rendez-vous"},
		Activated: []string{"prise-de-rendez-vous"},
		Getenv:    env(map[string]string{"CALENDAR_TZ": "Europe/Paris", "AUTO_BOOKING": "true"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := a.NewSession("s", "Europe/Paris", types.UserContext{})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.ActiveSkills(); len(got) != 1 || got[0] != "prise-de-rendez-vous" {
		t.Fatalf("skills actifs = %v", got)
	}
	if len(a.APIOptions) < 2 { // routes d'agenda + activation
		t.Fatalf("options d'API = %d", len(a.APIOptions))
	}
}

func TestConfigErrors(t *testing.T) {
	cases := []struct {
		name string
		cfg  app.Config
		want string
	}{
		{"skill inconnu", app.Config{Skills: []string{"facturation"}}, "inconnu"},
		{"actif non enregistré", app.Config{Skills: []string{"prise-de-rendez-vous"}, Activated: []string{"autre"}}, "ne fait pas partie"},
		{"fuseau absent", app.Config{Getenv: env(nil)}, "CALENDAR_TZ"},
		{"fuseau invalide", app.Config{Getenv: env(map[string]string{"CALENDAR_TZ": "Mars/Olympus"})}, "CALENDAR_TZ"},
		{"booléen invalide", app.Config{Getenv: env(map[string]string{"CALENDAR_TZ": "Europe/Paris", "AUTO_BOOKING": "oui"})}, "AUTO_BOOKING"},
		{"SearXNG absent", app.Config{Skills: []string{"prospect-research"}, Getenv: env(nil)}, "SEARXNG_URL"},
		{"TTS : moteur inconnu", app.Config{Getenv: valid(map[string]string{"TTS_PROVIDER": "espeak"})}, "TTS_PROVIDER"},
		{"TTS : URL absente", app.Config{Getenv: valid(map[string]string{"TTS_BASE_URL": ""})}, "TTS_BASE_URL"},
		{"TTS : voix mal formées", app.Config{Getenv: valid(map[string]string{"TTS_VOICES": "fr-FR"})}, "TTS_VOICES"},
		{"TTS : langue par défaut sans voix", app.Config{Getenv: valid(map[string]string{"TTS_DEFAULT_LANGUAGE": "de-DE"})}, "langue par défaut"},
		{"TTS : langue inconnue du moteur", app.Config{Getenv: valid(map[string]string{"TTS_VOICES": "fr-FR=ff_siwis;de-DE=x"})}, "de-DE"},
		{"TTS : format inconnu", app.Config{Getenv: valid(map[string]string{"TTS_DEFAULT_FORMAT": "aiff"})}, "TTS_DEFAULT_FORMAT"},
		{"TTS : vitesse incohérente", app.Config{Getenv: valid(map[string]string{"TTS_DEFAULT_SPEED": "3"})}, "vitesses"},
		{"TTS : délai invalide", app.Config{Getenv: valid(map[string]string{"TTS_TIMEOUT": "deux minutes"})}, "TTS_TIMEOUT"},
		{"SearXNG invalide", app.Config{Skills: []string{"prospect-research"}, Getenv: env(map[string]string{"SEARXNG_URL": "searxng:8080"})}, "SEARXNG_URL"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.cfg.Getenv == nil {
				c.cfg.Getenv = valid(nil)
			}
			_, err := app.New(scripted.New(), c.cfg)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("erreur = %v, attendu %q", err, c.want)
			}
		})
	}
}
