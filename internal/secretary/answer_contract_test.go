package secretary

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func words(n int, sentence string) string {
	var parts []string
	for countWords(strings.Join(parts, " ")) < n {
		parts = append(parts, sentence)
	}
	return strings.Join(parts, " ")
}

// replies serves the given model answers in order and counts the calls.
func replies(t *testing.T, answers ...string) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		i := calls
		if i >= len(answers) {
			i = len(answers) - 1
		}
		calls++
		body, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": answers[i]}}},
			"usage":   map[string]int{"prompt_tokens": 120, "completion_tokens": 30},
		})
		w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func TestShortAnswerIsLeftAlone(t *testing.T) {
	server, calls := replies(t, "El informe está listo y se envía hoy.")
	s := New(server.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	got, err := s.answerWithContext(context.Background(), "cómo va el informe", nil, nil, false, nil)
	if err != nil || got != "El informe está listo y se envía hoy." || *calls != 1 {
		t.Fatalf("got %q err=%v calls=%d", got, err, *calls)
	}
}

func TestLongAnswerIsRegeneratedOnceShorter(t *testing.T) {
	long := words(200, "Esta es una frase de relleno que alarga la respuesta sin aportar nada nuevo.")
	server, calls := replies(t, long, "El informe está listo; se envía hoy.")
	s := New(server.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	got, err := s.answerWithContext(context.Background(), "cómo va el informe", nil, nil, false, nil)
	if err != nil || got != "El informe está listo; se envía hoy." || *calls != 2 {
		t.Fatalf("got %q err=%v calls=%d", got, err, *calls)
	}
}

func TestStillLongAnswerIsTrimmedToWholeSentences(t *testing.T) {
	long := words(300, "Primera idea completa. Segunda idea completa con algo más de texto.")
	server, calls := replies(t, long, long)
	s := New(server.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	got, err := s.answerWithContext(context.Background(), "cómo va el informe", nil, nil, false, nil)
	if err != nil || *calls != 2 {
		t.Fatalf("err=%v calls=%d", err, *calls)
	}
	if limit := answerWordLimit("cómo va el informe", false) * 3 / 2; countWords(got) > limit {
		t.Fatalf("trimmed answer has %d words, limit %d", countWords(got), limit)
	}
	if !strings.HasSuffix(got, ".") {
		t.Fatalf("trimmed answer must end on a sentence: %q", got)
	}
}

func TestRegenerationFailureKeepsOriginalTrimmed(t *testing.T) {
	long := words(200, "Frase larga de relleno que no aporta datos nuevos a la conversación.")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		calls++
		if calls > 1 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": long}}}})
		w.Write(body)
	}))
	defer server.Close()
	s := New(server.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	got, err := s.answerWithContext(context.Background(), "cómo va el informe", nil, nil, false, nil)
	if err != nil || got == "" || countWords(got) > answerWordLimit("cómo va el informe", false)*3/2 {
		t.Fatalf("got %d words err=%v", countWords(got), err)
	}
}

func TestAnswerWordLimitByRequest(t *testing.T) {
	cases := []struct {
		question   string
		hasSources bool
		want       int
	}{
		{"qué tal el día", false, 70},
		{"cómo van las tareas", true, 90},
		{"explícame cómo funciona el sprint", false, 250},
		{"dame la lista completa de tareas", true, 250},
		{"analiza el estado del proyecto", true, 250},
	}
	for _, tc := range cases {
		if got := answerWordLimit(tc.question, tc.hasSources); got != tc.want {
			t.Errorf("%q => %d, want %d", tc.question, got, tc.want)
		}
	}
}

func TestTrimToSentencesKeepsFirstSentenceAndNeverCutsMidSentence(t *testing.T) {
	text := "Hola. " + words(100, "Una frase intermedia bastante larga para el recorte.")
	got := trimToSentences(text, 10)
	if !strings.HasPrefix(got, "Hola.") || strings.HasSuffix(got, "recor") {
		t.Fatalf("got %q", got)
	}
	single := words(120, "Una única frase interminable sin ningún punto intermedio") + "."
	if got := trimToSentences(single, 10); got != single {
		t.Fatalf("a single sentence must be kept whole, got %d words", countWords(got))
	}
}

func TestModelTokensAreRecordedFromBackendUsage(t *testing.T) {
	server, _ := replies(t, "Respuesta corta.")
	s := New(server.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	if _, err := s.answerWithContext(context.Background(), "hola qué tal", nil, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	families, err := s.VoiceMetrics().registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, family := range families {
		if family.GetName() == "lab_secretary_model_tokens_total" {
			for _, metric := range family.Metric {
				got[metric.Label[0].GetValue()] = metric.Counter.GetValue()
			}
		}
	}
	if got["prompt"] != 120 || got["completion"] != 30 {
		t.Fatalf("tokens = %v", got)
	}
}

// goldenAnswers is the reference set of conversations the answer layer must
// keep short. Each row is a question with the verbose reply a weak model gave
// in production; the contract must bring it inside the limit.
func TestGoldenConversationsStayWithinTheLengthContract(t *testing.T) {
	verbose := words(260, "Según la información disponible ambos han participado en actividades relacionadas con la revisión de plazos y la coordinación general.")
	questions := []struct {
		text       string
		hasSources bool
	}{
		{"dime cómo van Valentina y Gonzalo", true},
		{"qué tengo pendiente", true},
		{"resume la reunión de ayer", true},
		{"qué dijo Gustavo sobre el campo nuevo", true},
		{"cuál es el siguiente paso", false},
		{"cómo vamos con el sprint", false},
	}
	for i, q := range questions {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			server, _ := replies(t, verbose, verbose)
			s := New(server.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
			var sources []string
			if q.hasSources {
				sources = []string{"Jira: PMI-1 en curso", "Jira"}
			}
			got, err := s.answerWithContext(context.Background(), q.text, nil, nil, false, nil, sources...)
			if err != nil {
				t.Fatal(err)
			}
			limit := answerWordLimit(q.text, q.hasSources) * 3 / 2
			if n := countWords(got); n > limit || n == 0 {
				t.Fatalf("%q produced %d words, limit %d", q.text, n, limit)
			}
		})
	}
}
