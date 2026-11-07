package secretary

import (
	"context"
	"fmt"
	"net/mail"
	"sort"
	"strings"

	"github.com/stratecode/lab/internal/donnaidentity"
)

type MappingOption struct {
	Kind           donnaidentity.EntityKind
	CanonicalID    string
	CanonicalLabel string
	Provider       string
	ExternalID     string
	Evidence       []string
}

type MappingSourceReader interface {
	FindMappingOptions(context.Context, donnaidentity.EntityKind, string) ([]MappingOption, error)
}

func rankMappingOptions(options []MappingOption, query string) []MappingOption {
	needle := normalizeEntityAlias(query)
	filtered := make([]MappingOption, 0, len(options))
	for _, option := range options {
		if strings.TrimSpace(option.CanonicalID) == "" || strings.TrimSpace(option.CanonicalLabel) == "" || strings.TrimSpace(option.Provider) == "" || strings.TrimSpace(option.ExternalID) == "" {
			continue
		}
		label := normalizeEntityAlias(option.CanonicalLabel)
		if needle == "" || (label != needle && !strings.HasPrefix(label, needle) && !strings.Contains(label, needle)) {
			continue
		}
		filtered = append(filtered, option)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		a, b := normalizeEntityAlias(filtered[i].CanonicalLabel), normalizeEntityAlias(filtered[j].CanonicalLabel)
		score := func(label string) int {
			switch {
			case label == needle:
				return 0
			case strings.HasPrefix(label, needle):
				return 1
			case strings.Contains(label, needle):
				return 2
			default:
				return 3
			}
		}
		if score(a) != score(b) {
			return score(a) < score(b)
		}
		if a != b {
			return a < b
		}
		if filtered[i].Provider != filtered[j].Provider {
			return filtered[i].Provider < filtered[j].Provider
		}
		return filtered[i].ExternalID < filtered[j].ExternalID
	})
	return filtered
}

func (s *Service) guidedMapping(ctx context.Context, operatorID int64, channel, conversation, text string, progress func(string), observer InteractionObserver) (string, error) {
	principal, err := NewOperatorContext(operatorID, channel, conversation)
	if err != nil {
		return "No puedo gestionar mapeos sin una identidad autenticada y una conversación válida.", nil
	}
	if s.Identity == nil {
		return "El servicio de mapeos no está configurado.", nil
	}
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return mappingHelp(), nil
	}
	switch strings.ToLower(fields[1]) {
	case "confirmar":
		if len(fields) != 4 {
			return "Uso: /mapeo confirmar ID HUELLA_COMPLETA. La huella aparece junto al candidato.", nil
		}
		pending, err := s.Identity.ListMappings(ctx, principal, "", donnaidentity.MappingPending)
		if err != nil {
			return "No puedo consultar los mapeos pendientes ahora.", nil
		}
		for _, item := range pending {
			if item.ID != fields[2] || item.Channel != channel || item.ConversationID != conversation {
				continue
			}
			if len(item.PayloadDigest) == 0 || !strings.EqualFold(item.PayloadDigest, fields[3]) {
				return "La huella no coincide con el candidato mostrado. No he guardado el mapeo.", nil
			}
			approved, confirmErr := s.Identity.ConfirmMapping(ctx, principal, item.ID, item.PayloadDigest, true)
			if confirmErr != nil {
				return "No he podido confirmar el mapeo; sigue pendiente.", nil
			}
			return fmt.Sprintf("Mapeo confirmado: %s → %s (%s).", approved.CanonicalLabel, approved.CanonicalID, approved.Provider), nil
		}
		return "No encuentro ese candidato pendiente en esta conversación.", nil
	case "descartar":
		if len(fields) != 4 {
			return "Uso: /mapeo descartar ID HUELLA_COMPLETA.", nil
		}
		pending, err := s.Identity.ListMappings(ctx, principal, "", donnaidentity.MappingPending)
		if err != nil {
			return "No puedo consultar los mapeos pendientes ahora.", nil
		}
		for _, item := range pending {
			if item.ID == fields[2] && item.Channel == channel && item.ConversationID == conversation {
				if len(item.PayloadDigest) == 0 || !strings.EqualFold(item.PayloadDigest, fields[3]) {
					return "La huella no coincide; no he descartado el mapeo.", nil
				}
				if _, err := s.Identity.ConfirmMapping(ctx, principal, item.ID, item.PayloadDigest, false); err != nil {
					return "No he podido descartar el mapeo.", nil
				}
				return "Candidato descartado.", nil
			}
		}
		return "No encuentro ese candidato pendiente en esta conversación.", nil
	case "persona", "proyecto":
		if len(fields) < 3 {
			return "Indica un nombre o clave: /mapeo persona Valentina o /mapeo proyecto PMI.", nil
		}
		if source, ok := s.Sources.(MappingSourceReader); ok {
			if observer != nil {
				observer(InteractionEvent{Kind: EventLookupStarted, Meaning: InteractionLookup, Source: "Jira"})
			}
			if progress != nil {
				progress("Voy a buscar candidatos en Jira.")
			}
			kind := donnaidentity.EntityPerson
			if strings.EqualFold(fields[1], "proyecto") {
				kind = donnaidentity.EntityProject
			}
			query := strings.TrimSpace(strings.Join(fields[2:], " "))
			options, err := source.FindMappingOptions(ctx, kind, query)
			if err != nil {
				return "Jira no pudo verificar esos candidatos. No he creado mapeos.", nil
			}
			options = rankMappingOptions(options, query)
			if len(options) == 0 {
				return "No encontré candidatos con identificador verificable en Jira.", nil
			}
			if len(options) > 8 {
				options = options[:8]
			}
			created := make([]donnaidentity.MappingCandidate, 0, len(options))
			for _, option := range options {
				item, createErr := s.Identity.CreateCandidate(ctx, principal, donnaidentity.MappingCandidate{Kind: option.Kind, CanonicalID: option.CanonicalID, CanonicalLabel: option.CanonicalLabel, Provider: option.Provider, ExternalID: option.ExternalID, Evidence: option.Evidence})
				if createErr != nil {
					return "No he podido guardar todos los candidatos; ninguno ha sido aprobado. Repite la búsqueda para revisar el estado.", nil
				}
				created = append(created, item)
			}
			lines := []string{"He encontrado opciones en Jira. Siguen pendientes; confirma solo la correcta con su ID y huella:"}
			for i, item := range created {
				lines = append(lines, fmt.Sprintf("%d) %s — %s · Jira ID %s\n/mapeo confirmar %s %s", i+1, item.CanonicalLabel, item.CanonicalID, item.ExternalID, item.ID, item.PayloadDigest))
			}
			if kind == donnaidentity.EntityPerson {
				lines = append(lines, "Jira no ha proporcionado emails; no he añadido ninguno.")
			}
			return strings.Join(lines, "\n"), nil
		}
		return "La búsqueda guiada necesita Jira conectado con permisos de lectura.", nil
	case "email", "alias":
		return s.createUserSuppliedMapping(ctx, principal, fields)
	case "pendientes":
		items, err := s.Identity.ListMappings(ctx, principal, "", donnaidentity.MappingPending)
		if err != nil {
			return "No puedo consultar los mapeos pendientes ahora.", nil
		}
		lines := []string{}
		for _, item := range items {
			if item.Channel == channel && item.ConversationID == conversation {
				lines = append(lines, fmt.Sprintf("%s · %s: %s (%s) · huella %s", item.ID, item.Kind, item.CanonicalLabel, item.CanonicalID, item.PayloadDigest))
			}
		}
		if len(lines) == 0 {
			return "No hay candidatos pendientes en esta conversación.", nil
		}
		return strings.Join(lines, "\n"), nil
	default:
		return mappingHelp(), nil
	}
}

func (s *Service) createUserSuppliedMapping(ctx context.Context, principal OperatorContext, fields []string) (string, error) {
	separator := -1
	for i := 3; i < len(fields); i++ {
		if strings.EqualFold(fields[i], "para") {
			separator = i
			break
		}
	}
	if len(fields) < 5 || separator < 3 || separator+1 >= len(fields) {
		return "Uso: /mapeo email persona@empresa.com para ID_JIRA o /mapeo alias nombre corto para ID_JIRA.", nil
	}
	value, targetID := strings.TrimSpace(strings.Join(fields[2:separator], " ")), strings.TrimSpace(strings.Join(fields[separator+1:], " "))
	kind := donnaidentity.EntityEmail
	if strings.EqualFold(fields[1], "alias") {
		kind = donnaidentity.EntityAlias
	}
	if kind == donnaidentity.EntityEmail {
		parsed, err := mail.ParseAddress(value)
		if err != nil || parsed.Address != value {
			return "Ese email no tiene un formato válido; no he creado el candidato.", nil
		}
	}
	approved, err := s.Identity.ListMappings(ctx, principal, "", donnaidentity.MappingApproved)
	if err != nil {
		return "No puedo validar el ID canónico ahora.", nil
	}
	var target *donnaidentity.MappingCandidate
	allowedTargetKind := donnaidentity.EntityPerson
	if kind == donnaidentity.EntityAlias {
		allowedTargetKind = donnaidentity.EntityProject
	}
	for i := range approved {
		if (approved[i].Kind == donnaidentity.EntityPerson || (kind == donnaidentity.EntityAlias && approved[i].Kind == allowedTargetKind)) && (approved[i].CanonicalID == targetID || approved[i].ExternalID == targetID) {
			target = &approved[i]
			break
		}
	}
	if target == nil {
		return "El ID de Jira debe corresponder a una persona confirmada (email) o a una persona/proyecto confirmado (alias). Primero busca y confirma el mapeo base.", nil
	}
	item, err := s.Identity.CreateCandidate(ctx, principal, donnaidentity.MappingCandidate{Kind: kind, CanonicalID: target.CanonicalID, CanonicalLabel: target.CanonicalLabel, Provider: target.Provider, ExternalID: target.ExternalID, Aliases: []string{value}, Evidence: []string{"Dato proporcionado por el operador en esta conversación"}})
	if err != nil {
		return "No he podido guardar el candidato; sigue sin estar aprobado.", nil
	}
	return fmt.Sprintf("Candidato %s para %s → %s. Está pendiente de tu confirmación exacta:\n/mapeo confirmar %s %s", kind, value, target.CanonicalLabel, item.ID, item.PayloadDigest), nil
}

func mappingHelp() string {
	return "Mapeos guiados (todos requieren confirmación):\n/mapeo persona NOMBRE — buscar personas en Jira\n/mapeo proyecto TEXTO — buscar proyectos en Jira\n/mapeo email correo@dominio para ID_JIRA\n/mapeo alias APODO para ID_JIRA\n/mapeo pendientes — revisar esta conversación\n/mapeo confirmar ID HUELLA_COMPLETA — aprobar exactamente un candidato\n/mapeo descartar ID HUELLA_COMPLETA — descartar uno"
}
