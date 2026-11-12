import importlib.util
import json
from pathlib import Path
import tempfile
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

    def test_pass_and_receipts(self):
        result, output, calls = self.scenario([False, True])
        self.assertEqual(result["stop"], "pass")
        self.assertTrue(result["passed"])
        self.assertEqual(json.loads((output / "round-1.json").read_text()),
                         {"round": 1, "findings": 2, "gate": False, "cost": None})
        self.assertEqual(len([c for c in calls if c[0] == "claude"]), 2)
        self.assertIn("round-1.record.json", calls[7][-1])

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
