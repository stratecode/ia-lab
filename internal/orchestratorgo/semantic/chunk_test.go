package semantic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestChunkTextRespectsLimitsAndOverlap(t *testing.T) {
	input := strings.Repeat("abcdef ", 200)
	chunks := ChunkText(input, 80, 10, 3)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if len([]rune(chunk.ContentText)) > 80 {
			t.Fatalf("chunk exceeds max size: %d", len([]rune(chunk.ContentText)))
		}
		if chunk.ContentHash == "" {
			t.Fatal("chunk hash is empty")
		}
	}
}

func TestSanitizeTextRedactsSecrets(t *testing.T) {
	input := "normal\nAPI_KEY=abc123\n-----BEGIN PRIVATE KEY-----\nnext"
	output := SanitizeText(input)
	if strings.Contains(output, "abc123") || strings.Contains(output, "PRIVATE KEY") {
		t.Fatalf("secret leaked after sanitization: %q", output)
	}
	if !strings.Contains(output, "normal") || !strings.Contains(output, "next") {
		t.Fatalf("non-secret content was removed: %q", output)
	}
}

func TestEmbeddingClientRejectsMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	client := NewEmbeddingClient(server.URL, "test", "bge-m3", 1024, time.Second)
	_, err := client.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected malformed embedding response error")
	}
}

func TestEmbeddingClientRejectsDimensionMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2]}]}`))
	}))
	defer server.Close()

	client := NewEmbeddingClient(server.URL, "test", "bge-m3", 1024, time.Second)
	_, err := client.Embed(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "dimensions mismatch") {
		t.Fatalf("expected dimensions mismatch, got %v", err)
	}
}

func TestShouldSplitForEmbedding(t *testing.T) {
	longContent := strings.Repeat("token dense content ", 30)
	if !shouldSplitForEmbedding(&EmbeddingError{StatusCode: http.StatusInternalServerError}, longContent, 0) {
		t.Fatal("expected 500 response on long content to trigger split")
	}
	if shouldSplitForEmbedding(&EmbeddingError{StatusCode: http.StatusBadRequest}, longContent, 0) {
		t.Fatal("expected non-500 response to avoid split")
	}
	if shouldSplitForEmbedding(&EmbeddingError{StatusCode: http.StatusInternalServerError}, "tiny chunk", 0) {
		t.Fatal("expected tiny chunk to avoid split")
	}
	if shouldSplitForEmbedding(&EmbeddingError{StatusCode: http.StatusInternalServerError}, longContent, 4) {
		t.Fatal("expected depth guard to stop recursive split")
	}
}

func TestSplitChunkForEmbedding(t *testing.T) {
	content := "line one with enough text to split.\nline two keeps the chunk large enough.\nline three closes it."
	left, right := splitChunkForEmbedding(content)
	if left == "" || right == "" {
		t.Fatal("expected both split chunks to contain content")
	}
	if left == content || right == content {
		t.Fatal("expected split chunks to be smaller than original")
	}
	if strings.TrimSpace(left+" "+right) == "" {
		t.Fatal("expected non-empty split output")
	}
}

func TestSearchRequiresRetrievalScope(t *testing.T) {
	service := &Service{cfg: config.Config{SemanticEnabled: true}}
	_, err := service.Search(context.Background(), domain.SemanticSearchRequest{Query: "anything"})
	if err == nil || !strings.Contains(err.Error(), "requires initiative_id") {
		t.Fatalf("expected retrieval scope error, got %v", err)
	}
}
