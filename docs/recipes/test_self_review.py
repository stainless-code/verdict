import importlib.util
import json
import os
from pathlib import Path
import tempfile
import sys
import unittest

spec = importlib.util.spec_from_file_location("recipe", Path(__file__).with_name("self-review.py"))
recipe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(recipe)


class LoopTest(unittest.TestCase):
    def scenario(self, gates, failure=None, budget=10):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        output = Path(temporary.name) / "receipts"
        calls = []

        def call(argv, seconds, agent):
            calls.append(argv)
            if failure:
                raise recipe.Stop(failure)
            if agent:
                return None
            verb = argv[1]
            if verb == "run":
                return {"argv": ["claude", "-p", "review"], "binary": "claude"}
            if verb == "record":
                return {"findings": {"open": 2}}
            if verb == "gate":
                return {"passed": gates.pop(0)}
            return {}

        result = recipe.loop("acme/app", "claude", 2, budget, output,
                             call=call, clock=lambda: 0)
        return result, output, calls

    def test_process_refusals(self):
        scenarios = [
            ("print('broken')", False, "invalid-envelope"),
            ("raise SystemExit(7)", True, "agent-failed"),
            ('print(\'{"error":null,"data":{}}\'); raise SystemExit(3)', False, "tool-failed"),
            ("import time; time.sleep(10)", True, "budget"),
        ]
        for code, agent, expected in scenarios:
            with self.subTest(expected=expected):
                with self.assertRaisesRegex(recipe.Stop, "^" + expected + "$"):
                    recipe.execute([sys.executable, "-c", code], 0.1 if expected == "budget" else 10, agent)

    def test_pass_and_receipts(self):
        result, output, calls = self.scenario([False, True])
        self.assertEqual(result["stop"], "pass")
        self.assertTrue(result["passed"])
        self.assertEqual(json.loads((output / "round-1.json").read_text()),
                         {"round": 1, "findings": 2, "gate": False, "cost": None})
        self.assertEqual(len([c for c in calls if c[0] == "claude"]), 2)
        self.assertIn("round-1.record.json", calls[7][-1])

    def test_stale_retry_reads_findings(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        output = root / "receipts"
        bundle = root / ".verdict" / "state" / "local-sr-receipts-1"
        bundle.mkdir(parents=True)
        (bundle / "review.json").write_text(json.dumps(
            {"schema_version": "1", "findings": [{"path": "notes.txt", "line": 2}]}))
        records = []

        def call(argv, seconds, agent):
            if agent:
                return None
            verb = argv[1]
            if verb == "run":
                return {"argv": ["claude", "-p", "review"], "binary": "claude"}
            if verb == "record":
                records.append(argv)
                if len(records) == 1:
                    raise recipe.Stop("coverage-stale")
                return {"findings": {"open": 0}}
            if verb == "gate":
                return {"passed": True}
            return {}

        previous = Path.cwd()
        os.chdir(root)
        self.addCleanup(os.chdir, previous)
        result = recipe.loop("acme/app", "claude", 2, 10, output, call=call, clock=lambda: 0)

        self.assertEqual(result["stop"], "pass")
        stale = result["rounds"][0]
        self.assertTrue(stale["stale"])
        self.assertEqual(stale["findings"], 1)
        self.assertTrue((output / "round-1.findings.json").exists())

    def test_max_rounds(self):
        result, _, _ = self.scenario([False, False])
        self.assertEqual(result["stop"], "max-rounds")
        self.assertFalse(result["passed"])
        self.assertEqual(len(result["rounds"]), 2)

    def test_refusal(self):
        result, _, calls = self.scenario([], failure="unknown-agent")
        self.assertEqual(result["stop"], "unknown-agent")
        self.assertFalse(result["passed"])
        self.assertEqual(len(calls), 1)

    def test_budget(self):
        result, _, calls = self.scenario([], budget=0)
        self.assertEqual(result["stop"], "budget")
        self.assertFalse(result["passed"])
        self.assertEqual(calls, [])


if __name__ == "__main__":
    unittest.main()
