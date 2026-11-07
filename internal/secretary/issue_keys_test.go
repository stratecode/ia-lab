package secretary

import "testing"

func TestNormalizeIssueKeysHandlesHowPeopleSpeakThem(t *testing.T) {
	prefixes := []string{"PMI", "STRAT", "COC", "SW"}
	cases := map[string]string{
		"dame detalles de la STRAT 166":         "dame detalles de la STRAT-166",
		"dame detalles de la Strat 166.":        "dame detalles de la STRAT-166.",
		"y la estrat ciento sesenta y seis?":    "y la STRAT-166?",
		"cómo va la tarea PMI 67":               "cómo va la tarea PMI-67",
		"cómo va la tarea PMI60 y 7 hoy":        "cómo va la tarea PMI60 y 7 hoy",
		"cómo va la tarea PMI 60 y 7 hoy":       "cómo va la tarea PMI-67 hoy",
		"PMI guion 77":                          "PMI-77",
		"pe eme i sesenta y siete":              "PMI-67",
		"mira coc noventa y nueve":              "mira COC-99",
		"STRAT mil doscientos treinta y cuatro": "STRAT-1234",
		"SW 12 y PMI 5":                         "SW-12 y PMI-5",
		"¿Qué hay en la PMI 161?":               "¿Qué hay en la PMI-161?",
	}
	for in, want := range cases {
		if got := NormalizeIssueKeys(in, prefixes); got != want {
			t.Errorf("NormalizeIssueKeys(%q)\n got  %q\n want %q", in, got, want)
		}
	}
}

func TestNormalizeIssueKeysLeavesOtherTextAlone(t *testing.T) {
	prefixes := []string{"PMI", "STRAT"}
	for _, in := range []string{
		"qué tengo pendiente en Jira",
		"PMI-67 ya está bien escrito",
		"la estrategia de 2026",
		"strat",
		"PMI sin número",
		"El PMI es un indicador",
		"",
	} {
		if got := NormalizeIssueKeys(in, prefixes); got != in {
			t.Errorf("text changed: %q -> %q", in, got)
		}
	}
	if got := NormalizeIssueKeys("STRAT 166", nil); got != "STRAT 166" {
		t.Errorf("without prefixes nothing may change, got %q", got)
	}
}

// Only PMI is spelled by letters ("pe eme i"); people say the others as words.
func TestSpokenKeyRoundTrip(t *testing.T) {
	for _, key := range []string{"PMI-67", "PMI-1", "PMI-161"} {
		spoken := SpokenText("Mira " + key + " ahora")
		back := NormalizeIssueKeys(spoken, DefaultIssuePrefixes)
		if back != "Mira "+key+" ahora" {
			t.Errorf("%s -> %q -> %q", key, spoken, back)
		}
	}
}
