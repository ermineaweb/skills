package app

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"skills/api/ttsapi"
	"skills/services/tts"
	"skills/services/tts/kokoro"
	synthesevocale "skills/skills/synthese-vocale"
)

const ttsName = synthesevocale.Name

// ttsProviders associe chaque valeur de TTS_PROVIDER à la construction de
// son moteur. Ajouter un moteur (Piper, service cloud…) = ajouter une
// entrée ; le skill, ses tools et l'API ne changent pas.
var ttsProviders = map[string]func(Env) (tts.Engine, error){
	"kokoro": kokoroEngine,
}

// kokoroEngine : serveur Kokoro-FastAPI (service kokoro de compose.yaml).
//
//	TTS_BASE_URL   URL du serveur (obligatoire), ex: http://kokoro:8880
//	TTS_STREAMING  audio transmis phrase par phrase (true/false, défaut true)
func kokoroEngine(env Env) (tts.Engine, error) {
	base := env.Getenv("TTS_BASE_URL")
	if base == "" {
		return nil, fmt.Errorf("TTS_BASE_URL : %w", errMissing)
	}
	streaming := true
	if raw := env.Getenv("TTS_STREAMING"); raw != "" {
		var err error
		if streaming, err = strconv.ParseBool(raw); err != nil {
			return nil, fmt.Errorf("TTS_STREAMING : valeur %q invalide (true ou false)", raw)
		}
	}
	e, err := kokoro.New(kokoro.Config{BaseURL: base, Stream: streaming})
	if err != nil {
		return nil, fmt.Errorf("TTS_BASE_URL : %w", err)
	}
	return e, nil
}

// synthesisVoice construit le skill synthese-vocale avec la route de
// lecture des audios.
//
// Configuration (valeurs par défaut dans app.env) :
//
//	TTS_PROVIDER          moteur (obligatoire) : kokoro, plus sa configuration propre
//	TTS_VOICES            voix par langue (obligatoire), ex: fr-FR=ff_siwis;en-US=af_heart,am_adam
//	                      (la première voix d'une langue est sa voix par défaut)
//	TTS_DEFAULT_LANGUAGE  langue par défaut (obligatoire), présente dans TTS_VOICES
//	TTS_DEFAULT_FORMAT    mp3, opus ou wav (défaut mp3)
//	TTS_DEFAULT_SPEED, TTS_MIN_SPEED, TTS_MAX_SPEED   vitesse (défauts 1, 0.5, 2)
//	TTS_MAX_TEXT_LENGTH   caractères par lecture (défaut 2000)
//	TTS_TIMEOUT           durée maximale d'une synthèse (défaut 2m)
//	TTS_MAX_CONCURRENT    synthèses simultanées (défaut 2)
func synthesisVoice(env Env) (module, error) {
	provider := env.Getenv("TTS_PROVIDER")
	build, ok := ttsProviders[provider]
	if !ok {
		names := make([]string, 0, len(ttsProviders))
		for n := range ttsProviders {
			names = append(names, n)
		}
		slices.Sort(names)
		return module{}, fmt.Errorf("TTS_PROVIDER : moteur %q inconnu (disponibles : %s)", provider, strings.Join(names, ", "))
	}
	engine, err := build(env)
	if err != nil {
		return module{}, err
	}
	voices, err := parseVoices(env.Getenv("TTS_VOICES"))
	if err != nil {
		return module{}, fmt.Errorf("TTS_VOICES : %w", err)
	}
	cfg := tts.Config{Engine: engine, Voices: voices, DefaultLanguage: env.Getenv("TTS_DEFAULT_LANGUAGE")}
	if raw := env.Getenv("TTS_DEFAULT_FORMAT"); raw != "" {
		f, ok := tts.ParseFormat(raw)
		if !ok {
			return module{}, fmt.Errorf("TTS_DEFAULT_FORMAT : format %q inconnu (mp3, opus, wav)", raw)
		}
		cfg.DefaultFormat = f
	}
	for _, v := range []struct {
		name string
		dst  *float64
	}{{"TTS_DEFAULT_SPEED", &cfg.DefaultSpeed}, {"TTS_MIN_SPEED", &cfg.MinSpeed}, {"TTS_MAX_SPEED", &cfg.MaxSpeed}} {
		if raw := env.Getenv(v.name); raw != "" {
			f, err := strconv.ParseFloat(raw, 64)
			if err != nil || f <= 0 {
				return module{}, fmt.Errorf("%s : valeur %q invalide (nombre positif)", v.name, raw)
			}
			*v.dst = f
		}
	}
	for _, v := range []struct {
		name string
		dst  *int
	}{{"TTS_MAX_TEXT_LENGTH", &cfg.MaxTextLength}, {"TTS_MAX_CONCURRENT", &cfg.MaxConcurrent}} {
		if raw := env.Getenv(v.name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 {
				return module{}, fmt.Errorf("%s : valeur %q invalide (entier positif)", v.name, raw)
			}
			*v.dst = n
		}
	}
	if raw := env.Getenv("TTS_TIMEOUT"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return module{}, fmt.Errorf("TTS_TIMEOUT : valeur %q invalide (ex: 2m)", raw)
		}
		cfg.Timeout = d
	}
	svc, err := tts.NewService(cfg)
	if err != nil {
		return module{}, err
	}
	skill, err := synthesevocale.New(synthesevocale.Config{Service: svc})
	if err != nil {
		return module{}, err
	}
	return module{skill: skill, api: ttsapi.Options(svc)}, nil
}

// parseVoices lit « langue=voix1,voix2;langue2=voix3 ».
func parseVoices(raw string) (map[string][]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errMissing
	}
	out := map[string][]string{}
	for _, part := range strings.Split(raw, ";") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		lang, list, ok := strings.Cut(part, "=")
		lang = strings.TrimSpace(lang)
		voices := splitList(list)
		if !ok || lang == "" || len(voices) == 0 {
			return nil, fmt.Errorf("%q invalide (attendu langue=voix1,voix2)", part)
		}
		if _, dup := out[lang]; dup {
			return nil, fmt.Errorf("langue %s déclarée deux fois", lang)
		}
		out[lang] = voices
	}
	return out, nil
}
