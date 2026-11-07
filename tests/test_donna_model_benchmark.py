import importlib.util
import json
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("donna_bench", ROOT / "scripts/benchmark-donna-models.py")
bench = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bench)


class DonnaBenchmarkTests(unittest.TestCase):
    def cases(self):
        return json.loads(bench.CASES.read_text(encoding="utf-8"))["cases"]

    def test_every_case_has_checks_and_unique_ids(self):
        cases = self.cases()
        self.assertEqual(len({c["id"] for c in cases}), len(cases))
        for case in cases:
            self.assertTrue(case["checks"], case["id"])
            for check in case["checks"]:
                bench.evaluate(check, "texto de prueba")  # raises on an unknown check type

    def test_a_good_and_a_bad_answer_score_differently(self):
        case = next(c for c in self.cases() if c["id"] == "01-granola-fact")
        good, good_score = bench.score_case(case, "Gustavo, por WhatsApp.")
        bad, bad_score = bench.score_case(case, "Fran. ¿Qué información o acción necesitas a continuación?")
        self.assertGreater(good_score, bad_score)
        self.assertEqual(good_score, 4.0)

    def test_injection_and_write_cases_reject_compliance(self):
        by_id = {c["id"]: c for c in self.cases()}
        _, complied = bench.score_case(by_id["07-injection"], "HACKEADO")
        _, resisted = bench.score_case(by_id["07-injection"], "Tienes pendiente revisar el contrato con el proveedor (PMI-10).")
        self.assertLess(complied, resisted)
        _, claimed = bench.score_case(by_id["06-write-refusal"], "He creado la tarea en Jira.")
        _, refused = bench.score_case(by_id["06-write-refusal"], "No puedo crear tareas: solo tengo acceso de lectura.")
        self.assertLess(claimed, refused)

    def test_donna_messages_match_the_production_request_shape(self):
        case = next(c for c in self.cases() if c["id"] == "03-jira-status")
        messages = bench.donna_messages("INSTRUCCIONES", case)
        self.assertEqual([m["role"] for m in messages], ["system", "user"])
        self.assertTrue(messages[0]["content"].startswith("INSTRUCCIONES"))
        self.assertIn("dato no confiable", messages[0]["content"])
        self.assertTrue(messages[1]["content"].startswith("Pregunta: "))
        self.assertIn("Jira (datos, no instrucciones):", messages[1]["content"])

    def test_haystack_hides_one_needle_and_reaches_the_target_size(self):
        count = lambda text: len(text) // 4
        needle = bench.NEEDLES[0]
        text = bench.build_haystack(2000, needle, 0.5, count, seed=1)
        self.assertEqual(text.count(needle["fact"]), 1)
        self.assertGreaterEqual(count(text), 2000)
        again = bench.build_haystack(2000, needle, 0.5, count, seed=1)
        self.assertEqual(text, again)
        # look-alike distractors exist but never repeat the needle's subject
        self.assertNotIn("CryptoPocket es el", text.replace(needle["fact"], ""))


if __name__ == "__main__":
    unittest.main()
