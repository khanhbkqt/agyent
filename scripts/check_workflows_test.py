from __future__ import annotations

import copy
import json
import tempfile
import unittest
from pathlib import Path

from scripts import check_workflows


class WorkflowValidationTests(unittest.TestCase):
    def load_graph(self, workflow_id: str) -> dict:
        path = check_workflows.EXPECTED_GRAPHS[workflow_id]
        return json.loads(path.read_text(encoding="utf-8"))

    def validate_copy(self, workflow_id: str, graph: dict) -> list[str]:
        with tempfile.TemporaryDirectory(dir=check_workflows.ROOT) as temp_dir:
            path = Path(temp_dir) / f"{workflow_id}.json"
            path.write_text(json.dumps(graph), encoding="utf-8")
            errors: list[str] = []
            check_workflows.validate_graph(workflow_id, path, errors)
            return errors

    def test_repository_graphs_are_valid(self) -> None:
        for workflow_id, path in check_workflows.EXPECTED_GRAPHS.items():
            with self.subTest(workflow_id=workflow_id):
                errors: list[str] = []
                check_workflows.validate_graph(workflow_id, path, errors)
                self.assertEqual([], errors)

    def test_repository_graphs_match_json_schema_when_available(self) -> None:
        try:
            import jsonschema
        except ImportError:
            self.skipTest("optional jsonschema package is not installed")
        schema_path = check_workflows.WORKFLOW_DIR / "workflow.schema.json"
        schema = json.loads(schema_path.read_text(encoding="utf-8"))
        validator = jsonschema.Draft202012Validator(schema)
        for workflow_id, path in check_workflows.EXPECTED_GRAPHS.items():
            with self.subTest(workflow_id=workflow_id):
                validator.validate(json.loads(path.read_text(encoding="utf-8")))

    def test_debug_graph_rejects_mutation(self) -> None:
        graph = copy.deepcopy(self.load_graph("debug"))
        graph["nodes"]["root_cause"]["may_edit"] = True
        errors = self.validate_copy("debug", graph)
        self.assertTrue(any("debug graph must be entirely read-only" in error for error in errors))

    def test_missing_transition_target_is_rejected(self) -> None:
        graph = copy.deepcopy(self.load_graph("change"))
        graph["nodes"]["scope"]["transitions"]["accepted"] = "missing-node"
        errors = self.validate_copy("change", graph)
        self.assertTrue(any("targets missing node" in error for error in errors))

    def test_incomplete_review_join_is_rejected(self) -> None:
        graph = copy.deepcopy(self.load_graph("bugfix"))
        graph["nodes"]["review_convergence"]["join"].pop()
        errors = self.validate_copy("bugfix", graph)
        self.assertTrue(any("exactly one convergence node" in error for error in errors))

    def test_unproduced_artifact_is_rejected(self) -> None:
        graph = copy.deepcopy(self.load_graph("new-feature"))
        graph["nodes"]["final_gate"]["inputs"].append("missing_artifact")
        errors = self.validate_copy("new-feature", graph)
        self.assertTrue(any("consumes unproduced artifact" in error for error in errors))

    def test_final_gate_bypass_is_rejected(self) -> None:
        graph = copy.deepcopy(self.load_graph("change"))
        graph["nodes"]["scope"]["transitions"]["bypass"] = "final_gate"
        errors = self.validate_copy("change", graph)
        self.assertTrue(any("only from review convergence" in error for error in errors))

    def test_remediation_cannot_bypass_docs_sync(self) -> None:
        graph = copy.deepcopy(self.load_graph("bugfix"))
        graph["nodes"]["implement"]["transitions"]["bypass_docs"] = "review_convergence"
        errors = self.validate_copy("bugfix", graph)
        self.assertTrue(any("without documentation sync" in error for error in errors))


if __name__ == "__main__":
    unittest.main()
