package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func (s *Server) indexSemanticSource(w http.ResponseWriter, r *http.Request) {
	if s.Semantic == nil {
		writeDetail(w, http.StatusServiceUnavailable, "semantic service is not configured")
		return
	}
	var req domain.SemanticIndexRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	resp, err := s.Semantic.Index(r.Context(), req)
	if err != nil {
		writeDetail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) searchSemanticChunks(w http.ResponseWriter, r *http.Request) {
	if s.Semantic == nil {
		writeDetail(w, http.StatusServiceUnavailable, "semantic service is not configured")
		return
	}
	var req domain.SemanticSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	resp, err := s.Semantic.Search(r.Context(), req)
	if err != nil {
		writeDetail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) getSemanticChunk(w http.ResponseWriter, r *http.Request) {
	if s.Semantic == nil {
		writeDetail(w, http.StatusServiceUnavailable, "semantic service is not configured")
		return
	}
	chunkID := strings.TrimSpace(chi.URLParam(r, "chunkID"))
	if chunkID == "" {
		writeDetail(w, http.StatusBadRequest, "chunk id is required")
		return
	}
	chunk, err := s.Semantic.GetChunk(r.Context(), chunkID)
	if err != nil {
		writeDetail(w, http.StatusBadRequest, err.Error())
		return
	}
	if chunk == nil {
		writeDetail(w, http.StatusNotFound, "semantic chunk not found")
		return
	}
	writeJSON(w, http.StatusOK, chunk)
}

func (s *Server) buildContextPackage(w http.ResponseWriter, r *http.Request) {
	if s.ContextBuilder == nil {
		writeDetail(w, http.StatusServiceUnavailable, "context builder is not configured")
		return
	}
	var req domain.ContextBuildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	resp, err := s.ContextBuilder.Build(r.Context(), req)
	if err != nil {
		writeDetail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
