package app

import (
	"skills/services/agenda"
	agendaskill "skills/skills/agenda"
)

const agendaName = agendaskill.Name

// agendaSkill construit le skill agenda sur un agenda en mémoire, partagé
// par toutes les sessions : chaque utilisateur (UserContext.ClientID) n'y
// voit que ses événements. Les événements sont perdus au redémarrage.
//
// Aucune configuration : pour un agenda réel (Google Calendar, CalDAV…),
// implémenter agenda.Provider et le passer ici.
func agendaSkill(Env) (module, error) {
	skill, err := agendaskill.New(agendaskill.Config{Provider: agenda.NewMockProvider()})
	if err != nil {
		return module{}, err
	}
	return module{skill: skill}, nil
}
