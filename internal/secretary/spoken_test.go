package secretary

import "testing"

func TestSpokenTextSpellsNumbersKeysDatesAndPercentages(t *testing.T) {
	cases := map[string]string{
		"PMI-67":             "pe eme i sesenta y siete",
		"Mira STRAT-166 hoy": "Mira ese te erre a te ciento sesenta y seis hoy",
		"Tienes al menos 25 abiertas: 3 en curso, 3 bloqueadas y 19 por hacer.": "Tienes al menos veinticinco abiertas: tres en curso, tres bloqueadas y diecinueve por hacer.",
		"1 abierta":            "una abierta",
		"1 en curso":           "uno en curso",
		"2 vencidas":           "dos vencidas",
		"Vence el 2026-11-12.": "Vence el doce de noviembre de dos mil veintiséis.",
		"Acierto del 81 %":     "Acierto del ochenta y uno por ciento",
		"Son 3,5 horas":        "Son tres coma cinco horas",
		"Cuesta 1.500 €":       "Cuesta mil quinientos euros",
		"Cuesta 1 €":           "Cuesta un euro",
		"A las 14:30":          "A las catorce y treinta",
		"Reunión a las 9:00":   "Reunión a las nueve en punto",
	}
	for in, want := range cases {
		if got := SpokenText(in); got != want {
			t.Errorf("SpokenText(%q)\n got  %q\n want %q", in, got, want)
		}
	}
}

func TestSpokenTextRemovesMarkdownLinksEmojisAndRedactions(t *testing.T) {
	cases := map[string]string{
		"**Hecho.** Mira [aquí](https://x.example/z) y https://a.example/b": "Hecho. Mira aquí y un enlace",
		"🔍 Voy a mirar Jira.":              "Voy a mirar Jira.",
		"- uno\n- dos\n- tres":             "uno. dos. tres",
		"## Título\n\nTexto con `código`":  "Título. Texto con código",
		"El DNI es [REDACTED:national_id]": "El DNI es dato oculto",
		"Cierre   con   espacios .":        "Cierre con espacios.",
	}
	for in, want := range cases {
		if got := SpokenText(in); got != want {
			t.Errorf("SpokenText(%q)\n got  %q\n want %q", in, got, want)
		}
	}
}

func TestSpokenCardinal(t *testing.T) {
	cases := map[int]string{0: "cero", 16: "dieciséis", 21: "veintiuno", 31: "treinta y uno", 100: "cien", 101: "ciento uno", 999: "novecientos noventa y nueve",
		1000: "mil", 2026: "dos mil veintiséis", 21000: "veintiuno mil", 1_000_000: "un millón", 2_500_000: "dos millones quinientos mil"}
	for n, want := range cases {
		if got := spokenCardinal(n, false); got != want {
			t.Errorf("%d => %q, want %q", n, got, want)
		}
	}
	if got := spokenCardinal(201, true); got != "doscientas una" {
		t.Errorf("feminine 201 = %q", got)
	}
}

func TestSpokenTextLeavesPlainSpanishUntouched(t *testing.T) {
	in := "Gustavo enviará la propuesta por WhatsApp. Fran la parametrizará después."
	if got := SpokenText(in); got != in {
		t.Errorf("plain text changed: %q", got)
	}
}
