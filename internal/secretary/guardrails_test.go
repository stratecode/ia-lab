package secretary

import (
	"strings"
	"testing"
)

func TestRedactSensitiveDetectsValidatedSecretsAndIDs(t *testing.T) {
	cases := []struct {
		name, input, category string
	}{
		{"aws key", "clave AKIA" + "IOSFODNN7EXAMPLE usada", guardToken},
		{"github token", "token gh" + "p_abcdefghijklmnopqrstuvwxyz0123456789", guardToken},
		{"jwt", "ey" + "JhbGciOiJIUzI1NiJ9.ey" + "JzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghijk", guardToken},
		{"bearer", "Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345", guardToken},
		{"private key", "-----BEGIN RSA PRIV" + "ATE KEY-----\nMIIEabc\n-----END RSA PRIV" + "ATE KEY-----", guardPrivateKey},
		{"card luhn", "tarjeta 4111 1111 1111 1111 caducada", guardCard},
		{"iban spaced", "cuenta ES91 2100 0418 4502 0005 1332", guardIBAN},
		{"dni", "DNI 12345678Z del cliente", guardNationalID},
		{"nie", "NIE X1234567L", guardNationalID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, report := RedactSensitive(tc.input)
			if report[tc.category] != 1 {
				t.Fatalf("category %s = %d, want 1 (report %v, out %q)", tc.category, report[tc.category], report, out)
			}
			if !strings.Contains(out, guardMarker(tc.category)) {
				t.Fatalf("marker missing in %q", out)
			}
		})
	}
}

func TestRedactSensitiveLeavesOrdinaryWorkDataAlone(t *testing.T) {
	inputs := []string{
		"PMI-1234 vence el 2026-11-06 y hay 12345678 mensajes",
		"Contacta a valentina@stratecode.com o al +34 600 123 456",
		"Pedido 1234 5678 9012 3456 sin checksum válido",
		"DNI 12345678A con letra incorrecta",
		"ES00 0000 0000 0000 0000 0000 no valida",
		"1111 1111 1111 1111 placeholder",
		"",
	}
	for _, input := range inputs {
		out, report := RedactSensitive(input)
		if out != input || report.Total() != 0 {
			t.Fatalf("%q was altered to %q (%v)", input, out, report)
		}
	}
}

func TestGuardTurnsDoesNotMutateHistory(t *testing.T) {
	turns := []Turn{{Role: "user", Content: "mi DNI es 12345678Z"}, {Role: "assistant", Content: "ok"}}
	guarded, report := guardTurns(turns)
	if turns[0].Content != "mi DNI es 12345678Z" {
		t.Fatal("original turns were mutated")
	}
	if strings.Contains(guarded[0].Content, "12345678Z") || report[guardNationalID] != 1 {
		t.Fatalf("guarded = %q report = %v", guarded[0].Content, report)
	}
	if guarded[0].Role != "user" || guarded[1].Content != "ok" {
		t.Fatal("role or untouched turn changed")
	}
}

func TestGuardrailMetricsUseClosedCategoriesWithoutContent(t *testing.T) {
	metrics := NewVoiceMetrics()
	_, report := RedactSensitive("AKIA" + "IOSFODNN7EXAMPLE y 12345678Z")
	metrics.ObserveGuardrail(report)
	metrics.ObserveGuardrail(GuardrailReport{"user_supplied_label": 5})
	families, err := metrics.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "lab_secretary_guardrail_redactions_total" {
			continue
		}
		for _, metric := range family.Metric {
			found[metric.Label[0].GetValue()] = metric.Counter.GetValue()
		}
	}
	if found[guardToken] != 1 || found[guardNationalID] != 1 || len(found) != len(guardrailCategories) {
		t.Fatalf("metrics = %v", found)
	}
}
