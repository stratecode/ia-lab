import importlib.util
import json
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "llm_capability_benchmark", ROOT / "scripts/benchmark-llm-capabilities.py"
)
benchmark = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(benchmark)


class ParseStreamTests(unittest.TestCase):
    def test_collects_text_usage_finish_reason_and_first_token_time(self):
        start = 100.0
        lines = [
            'data: {"choices":[{"delta":{"reasoning_content":"Pensando"},"finish_reason":null}]}',
            "",
            'data: {"choices":[{"delta":{"content":"Hola mundo"},"finish_reason":"stop"}]}',
            "",
            'data: {"choices":[],"usage":{"prompt_tokens":17,"completion_tokens":2}}',
            "",
            "data: [DONE]",
        ]

        result = benchmark.parse_stream(lines, start, clock=lambda: 100.25)

        self.assertEqual(result["response"], "Hola mundo")
        self.assertEqual(result["reasoning_characters"], len("Pensando"))
        self.assertEqual(result["finish_reason"], "stop")
        self.assertEqual(result["prompt_tokens"], 17)
        self.assertEqual(result["completion_tokens"], 2)
        self.assertEqual(result["ttft_seconds"], 0.25)


class ModelOrderTests(unittest.TestCase):
    def test_rotates_first_model_across_cases(self):
        models = ["main", "qwen-eval", "lfm-2.6b", "qwen25-7b-q4_0"]

        self.assertEqual(benchmark.model_order(models, 0), models)
        self.assertEqual(benchmark.model_order(models, 1), models[1:] + models[:1])
        self.assertEqual(benchmark.model_order(models, 4), models)


class BenchmarkDatasetTests(unittest.TestCase):
    def test_dataset_has_ten_unique_versioned_cases_with_rubrics(self):
        dataset = json.loads((ROOT / "benchmarks/llm-capabilities/v1/cases.json").read_text())

        self.assertEqual(dataset["version"], "1.0")
        self.assertEqual(len(dataset["cases"]), 10)
        self.assertEqual(len({case["id"] for case in dataset["cases"]}), 10)
        self.assertTrue(all(case["max_tokens"] == 1536 for case in dataset["cases"]))
        for case in dataset["cases"]:
            self.assertTrue(case["messages"], case["id"])
            self.assertTrue(case["rubric"], case["id"])


if __name__ == "__main__":
    unittest.main()
