package donnaidentity

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxRequestBytes = 16 * 1024

type Authenticator interface {
	Authenticate(*http.Request) (Principal, error)
}

type HeaderAuthenticator struct {
	Token string
}

func (a HeaderAuthenticator) Authenticate(request *http.Request) (Principal, error) {
	provided := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
	if provided == "" || len(provided) != len(a.Token) || subtle.ConstantTimeCompare([]byte(provided), []byte(a.Token)) != 1 {
		return Principal{}, errors.New("unauthorized")
	}
	operatorID, err := strconv.ParseInt(strings.TrimSpace(request.Header.Get("X-Operator-ID")), 10, 64)
	if err != nil {
		return Principal{}, errors.New("unauthorized")
	}
	principal := Principal{
		OperatorID:     operatorID,
		Channel:        strings.TrimSpace(request.Header.Get("X-Channel")),
		ConversationID: strings.TrimSpace(request.Header.Get("X-Conversation-ID")),
	}
	if err := validatePrincipal(principal); err != nil {
		return Principal{}, errors.New("unauthorized")
	}
	return principal, nil
}

func validatePrincipal(principal Principal) error {
	if principal.OperatorID <= 0 || principal.ConversationID == "" {
		return ErrInvalidMapping
	}
	if principal.Channel != "telegram" && principal.Channel != "openwebui" {
		return ErrInvalidMapping
	}
	return nil
}

type createMappingRequest struct {
	Kind           EntityKind `json:"kind"`
	CanonicalID    string     `json:"canonical_id"`
	CanonicalLabel string     `json:"canonical_label"`
	Provider       string     `json:"provider"`
	ExternalID     string     `json:"external_id"`
	Aliases        []string   `json:"aliases,omitempty"`
	Evidence       []string   `json:"evidence,omitempty"`
}

type confirmMappingRequest struct {
	PayloadDigest string `json:"payload_digest"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func NewHandler(store Store, authenticator Authenticator) http.Handler {
	mux := http.NewServeMux()
	handler := &mappingHandler{store: store, authenticator: authenticator}
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/mappings", handler.create)
	mux.HandleFunc("GET /v1/mappings", handler.list)
	mux.HandleFunc("GET /v1/mappings/{id}", handler.get)
	mux.HandleFunc("POST /v1/mappings/{id}/confirm", handler.confirm)
	mux.HandleFunc("POST /v1/mappings/{id}/reject", handler.reject)
	return mux
}

type mappingHandler struct {
	store         Store
	authenticator Authenticator
}

func (h *mappingHandler) create(writer http.ResponseWriter, request *http.Request) {
	principal, ok := h.principal(writer, request)
	if !ok {
		return
	}
	var input createMappingRequest
	if err := decodeJSON(writer, request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid request body")
		return
	}
	candidate, err := h.store.CreateCandidate(request.Context(), MappingCandidate{
		OperatorID:     principal.OperatorID,
		Channel:        principal.Channel,
		ConversationID: principal.ConversationID,
		Kind:           input.Kind,
		CanonicalID:    input.CanonicalID,
		CanonicalLabel: input.CanonicalLabel,
		Provider:       input.Provider,
		ExternalID:     input.ExternalID,
		Aliases:        input.Aliases,
		Evidence:       input.Evidence,
		Status:         MappingPending,
	})
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, candidate)
}

func (h *mappingHandler) list(writer http.ResponseWriter, request *http.Request) {
	principal, ok := h.principal(writer, request)
	if !ok {
		return
	}
	kind := EntityKind(strings.TrimSpace(request.URL.Query().Get("kind")))
	status := MappingStatus(strings.TrimSpace(request.URL.Query().Get("status")))
	if !validKindOrEmpty(kind) || !validStatusOrEmpty(status) {
		writeError(writer, http.StatusBadRequest, "invalid mapping filter")
		return
	}
	items, err := h.store.List(request.Context(), principal.OperatorID, kind, status)
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func (h *mappingHandler) get(writer http.ResponseWriter, request *http.Request) {
	principal, ok := h.principal(writer, request)
	if !ok {
		return
	}
	item, err := h.store.Get(request.Context(), principal.OperatorID, request.PathValue("id"))
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (h *mappingHandler) confirm(writer http.ResponseWriter, request *http.Request) {
	h.setStatus(writer, request, MappingApproved)
}

func (h *mappingHandler) reject(writer http.ResponseWriter, request *http.Request) {
	h.setStatus(writer, request, MappingRejected)
}

func (h *mappingHandler) setStatus(writer http.ResponseWriter, request *http.Request, status MappingStatus) {
	principal, ok := h.principal(writer, request)
	if !ok {
		return
	}
	var input confirmMappingRequest
	if err := decodeJSON(writer, request, &input); err != nil || strings.TrimSpace(input.PayloadDigest) == "" {
		writeError(writer, http.StatusBadRequest, "invalid confirmation")
		return
	}
	item, err := h.store.SetStatus(request.Context(), principal.OperatorID, request.PathValue("id"), principal.Channel, principal.ConversationID, input.PayloadDigest, status, principal.OperatorID, nowUTC())
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (h *mappingHandler) principal(writer http.ResponseWriter, request *http.Request) (Principal, bool) {
	if h.authenticator == nil || h.store == nil {
		writeError(writer, http.StatusServiceUnavailable, "identity service unavailable")
		return Principal{}, false
	}
	principal, err := h.authenticator.Authenticate(request)
	if err != nil || validatePrincipal(principal) != nil {
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return Principal{}, false
	}
	return principal, true
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request contains trailing data")
	}
	return nil
}

func writeStoreError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidMapping):
		writeError(writer, http.StatusBadRequest, "invalid mapping")
	case errors.Is(err, ErrMappingNotFound):
		writeError(writer, http.StatusNotFound, "mapping not found")
	case errors.Is(err, ErrMappingConflict):
		writeError(writer, http.StatusConflict, "mapping confirmation conflict")
	default:
		writeError(writer, http.StatusInternalServerError, "identity service error")
	}
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, errorResponse{Error: message})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func validKindOrEmpty(kind EntityKind) bool {
	return kind == "" || kind == EntityPerson || kind == EntityEmail || kind == EntityProject || kind == EntityAlias
}

func validStatusOrEmpty(status MappingStatus) bool {
	return status == "" || status == MappingPending || status == MappingApproved || status == MappingRejected
}

var nowUTC = func() time.Time { return time.Now().UTC() }
