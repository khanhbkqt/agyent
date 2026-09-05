#!/usr/bin/env python3
"""Validate executable engineering workflow graphs without third-party packages."""

from __future__ import annotations

import json
import sys
from collections import deque
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
WORKFLOW_DIR = ROOT / ".agents" / "workflows"
EXPECTED_GRAPHS = {
    "debug": WORKFLOW_DIR / "debug.json",
    "bugfix": WORKFLOW_DIR / "bugfix.json",
    "change": WORKFLOW_DIR / "change.json",
    "new-feature": WORKFLOW_DIR / "new-feature.json",
}
REQUIRED_TOP_LEVEL = {
    "$schema",
    "schema_version",
    "id",
    "title",
    "intent",
    "instruction_priority",
    "entrypoint",
    "terminal_nodes",
    "documentation_node",
    "final_gate",
    "required_reviewers",
    "review_policy",
    "nodes",
}
REQUIRED_NODE_FIELDS = {
    "kind",
    "may_edit",
    "system_prompt",
    "guide",
    "skills",
    "tools",
    "inputs",
    "outputs",
    "gate",
    "transitions",
}
EXTERNAL_INPUTS = {"user_report", "user_request"}
BLOCKING_SEVERITIES = {"P0", "P1", "P2"}
ORCHESTRATOR_SKILL = "agyent-engineering-workflow"
ALLOWED_NODE_KINDS = {
    "intake",
    "evidence",
    "analysis",
    "design",
    "implementation",
    "verification",
    "documentation",
    "review",
    "convergence",
    "final",
}
ALLOWED_NODE_FIELDS = REQUIRED_NODE_FIELDS | {"join"}


def load_json(path: Path, errors: list[str]) -> dict[str, Any] | None:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        errors.append(f"{path.relative_to(ROOT)}: invalid JSON: {exc}")
        return None
    if not isinstance(value, dict):
        errors.append(f"{path.relative_to(ROOT)}: root must be an object")
        return None
    return value


def targets(transitions: Any, rel: Path, node_id: str, errors: list[str]) -> list[str]:
    if not isinstance(transitions, dict):
        errors.append(f"{rel}: node {node_id!r} transitions must be an object")
        return []
    result: list[str] = []
    for outcome, value in transitions.items():
        if not isinstance(outcome, str) or not outcome:
            errors.append(f"{rel}: node {node_id!r} has an empty transition outcome")
        if isinstance(value, str) and value:
            result.append(value)
        elif isinstance(value, list) and value and all(isinstance(item, str) and item for item in value):
            if len(set(value)) != len(value):
                errors.append(f"{rel}: node {node_id!r} transition {outcome!r} repeats a target")
            result.extend(value)
        else:
            errors.append(f"{rel}: node {node_id!r} transition {outcome!r} has invalid targets")
    return result


def reachable_from(start: str, adjacency: dict[str, list[str]]) -> set[str]:
    seen: set[str] = set()
    pending = deque([start])
    while pending:
        current = pending.popleft()
        if current in seen:
            continue
        seen.add(current)
        pending.extend(adjacency.get(current, []))
    return seen


def reachable_avoiding(start: str, adjacency: dict[str, list[str]], excluded: set[str]) -> set[str]:
    if start in excluded:
        return set()
    seen: set[str] = set()
    pending = deque([start])
    while pending:
        current = pending.popleft()
        if current in seen or current in excluded:
            continue
        seen.add(current)
        pending.extend(adjacency.get(current, []))
    return seen


def available_skills() -> set[str]:
    skill_paths = list((ROOT / ".agents" / "skills").glob("*/SKILL.md"))
    skill_paths.extend((ROOT / "builtin" / "plugins").glob("*/skills/*/SKILL.md"))
    return {path.parent.name for path in skill_paths}


def validate_graph(expected_id: str, path: Path, errors: list[str]) -> None:
    graph = load_json(path, errors)
    if graph is None:
        return
    rel = path.relative_to(ROOT)

    missing = REQUIRED_TOP_LEVEL - graph.keys()
    if missing:
        errors.append(f"{rel}: missing top-level fields {sorted(missing)}")
        return
    unexpected = graph.keys() - REQUIRED_TOP_LEVEL
    if unexpected:
        errors.append(f"{rel}: unsupported top-level fields {sorted(unexpected)}")
    if graph["schema_version"] != 1:
        errors.append(f"{rel}: schema_version must be 1")
    if graph["$schema"] != "./workflow.schema.json":
        errors.append(f"{rel}: $schema must reference './workflow.schema.json'")
    if graph["id"] != expected_id:
        errors.append(f"{rel}: id must be {expected_id!r}")
    priority = graph["instruction_priority"]
    if not isinstance(priority, str) or "does not grant" not in priority:
        errors.append(f"{rel}: instruction_priority must explicitly deny additional authority")

    nodes = graph["nodes"]
    if not isinstance(nodes, dict) or not nodes:
        errors.append(f"{rel}: nodes must be a non-empty object")
        return

    entrypoint = graph["entrypoint"]
    terminals = graph["terminal_nodes"]
    docs_node = graph["documentation_node"]
    final_gate = graph["final_gate"]
    reviewers = graph["required_reviewers"]
    for label, node_id in (("entrypoint", entrypoint), ("documentation_node", docs_node), ("final_gate", final_gate)):
        if not isinstance(node_id, str) or node_id not in nodes:
            errors.append(f"{rel}: {label} references missing node {node_id!r}")
    if not isinstance(terminals, list) or not terminals:
        errors.append(f"{rel}: terminal_nodes must be a non-empty list")
        terminals = []
    if not isinstance(reviewers, list) or len(reviewers) < (2 if expected_id == "debug" else 3):
        errors.append(f"{rel}: insufficient required_reviewers for strict cross-review")
        reviewers = []

    review_policy = graph["review_policy"]
    if not isinstance(review_policy, dict):
        errors.append(f"{rel}: review_policy must be an object")
    else:
        severities = review_policy.get("blocking_severities")
        if not isinstance(severities, list) or not BLOCKING_SEVERITIES.issubset(severities):
            errors.append(f"{rel}: P0, P1, and P2 must all block convergence")
        finding_schema = review_policy.get("finding_schema")
        required_finding_fields = {"id", "severity", "location", "evidence", "impact", "required_action"}
        if not isinstance(finding_schema, list) or not required_finding_fields.issubset(finding_schema):
            errors.append(f"{rel}: finding_schema is missing required fields")

    produced: set[str] = set()
    producers: dict[str, set[str]] = {}
    adjacency: dict[str, list[str]] = {}
    convergence_nodes: list[str] = []
    known_skills = available_skills()
    for node_id, node in nodes.items():
        if not isinstance(node, dict):
            errors.append(f"{rel}: node {node_id!r} must be an object")
            continue
        missing_node = REQUIRED_NODE_FIELDS - node.keys()
        if missing_node:
            errors.append(f"{rel}: node {node_id!r} missing fields {sorted(missing_node)}")
            continue
        unexpected_node = node.keys() - ALLOWED_NODE_FIELDS
        if unexpected_node:
            errors.append(f"{rel}: node {node_id!r} has unsupported fields {sorted(unexpected_node)}")
        if node["kind"] not in ALLOWED_NODE_KINDS:
            errors.append(f"{rel}: node {node_id!r} has unsupported kind {node['kind']!r}")

        prompt = node["system_prompt"]
        if not isinstance(prompt, str) or len(prompt.strip()) < 40:
            errors.append(f"{rel}: node {node_id!r} needs a substantive system_prompt")
        for list_field in ("guide", "skills", "tools", "outputs"):
            value = node[list_field]
            if not isinstance(value, list) or not value or not all(isinstance(item, str) and item for item in value):
                errors.append(f"{rel}: node {node_id!r} {list_field} must be a non-empty string list")
        if isinstance(node["skills"], list) and ORCHESTRATOR_SKILL not in node["skills"]:
            errors.append(f"{rel}: node {node_id!r} must include {ORCHESTRATOR_SKILL!r}")
        if isinstance(node["skills"], list):
            for skill in node["skills"]:
                if skill not in known_skills:
                    errors.append(f"{rel}: node {node_id!r} references unknown project skill {skill!r}")
        if not isinstance(node["inputs"], list) or not all(isinstance(item, str) and item for item in node["inputs"]):
            errors.append(f"{rel}: node {node_id!r} inputs must be a string list")
        if not isinstance(node["may_edit"], bool):
            errors.append(f"{rel}: node {node_id!r} may_edit must be boolean")
        elif node["may_edit"] and node["kind"] not in {"implementation", "documentation"}:
            errors.append(f"{rel}: node {node_id!r} may edit only in implementation/documentation stages")
        if expected_id == "debug" and node.get("may_edit"):
            errors.append(f"{rel}: debug graph must be entirely read-only")

        outputs = node["outputs"] if isinstance(node["outputs"], list) else []
        produced.update(item for item in outputs if isinstance(item, str))
        for artifact in outputs:
            if isinstance(artifact, str):
                producers.setdefault(artifact, set()).add(node_id)
        gate = node["gate"]
        if not isinstance(gate, dict):
            errors.append(f"{rel}: node {node_id!r} gate must be an object")
        else:
            requires = gate.get("requires")
            acceptance = gate.get("acceptance")
            if not isinstance(requires, list) or not requires:
                errors.append(f"{rel}: node {node_id!r} gate.requires must be non-empty")
            elif not set(requires).issubset(outputs):
                errors.append(f"{rel}: node {node_id!r} gate may require only its declared outputs")
            if not isinstance(acceptance, list) or not acceptance:
                errors.append(f"{rel}: node {node_id!r} gate.acceptance must be non-empty")

        node_targets = targets(node["transitions"], rel, node_id, errors)
        adjacency[node_id] = node_targets
        for target in node_targets:
            if target not in nodes:
                errors.append(f"{rel}: node {node_id!r} targets missing node {target!r}")

        is_terminal = node_id in terminals
        if is_terminal and node_targets:
            errors.append(f"{rel}: terminal node {node_id!r} must not transition")
        if not is_terminal and not node_targets:
            errors.append(f"{rel}: non-terminal node {node_id!r} must transition")
        if node["kind"] == "review":
            if node["may_edit"]:
                errors.append(f"{rel}: review node {node_id!r} must be read-only")
            if "do not edit" not in prompt.lower():
                errors.append(f"{rel}: review node {node_id!r} prompt must explicitly say 'Do not edit'")
        if node["kind"] == "convergence":
            convergence_nodes.append(node_id)

    for node_id, node in nodes.items():
        if not isinstance(node, dict) or not isinstance(node.get("inputs"), list):
            continue
        for artifact in node["inputs"]:
            if artifact not in produced and artifact not in EXTERNAL_INPUTS:
                errors.append(f"{rel}: node {node_id!r} consumes unproduced artifact {artifact!r}")
            elif artifact in producers and not any(
                node_id in reachable_from(producer, adjacency) for producer in producers[artifact]
            ):
                errors.append(
                    f"{rel}: node {node_id!r} consumes {artifact!r} only from a producer that cannot precede it"
                )

    for terminal in terminals:
        if terminal not in nodes:
            errors.append(f"{rel}: terminal node {terminal!r} does not exist")
    if final_gate in nodes and final_gate not in terminals:
        errors.append(f"{rel}: final_gate must be a terminal node")
    if final_gate in nodes:
        if nodes[final_gate].get("kind") != "final" or nodes[final_gate].get("may_edit"):
            errors.append(f"{rel}: final_gate must be a read-only final node")
    if docs_node in nodes and nodes[docs_node].get("kind") != "documentation":
        errors.append(f"{rel}: documentation_node must have kind 'documentation'")

    for reviewer in reviewers:
        node = nodes.get(reviewer)
        if not isinstance(node, dict) or node.get("kind") != "review":
            errors.append(f"{rel}: required reviewer {reviewer!r} is missing or not a review node")

    matching_convergence = []
    for node_id in convergence_nodes:
        join = nodes[node_id].get("join")
        if isinstance(join, list) and set(join) == set(reviewers):
            matching_convergence.append(node_id)
    if len(matching_convergence) != 1:
        errors.append(f"{rel}: exactly one convergence node must join all required reviewers")
    else:
        convergence = matching_convergence[0]
        for reviewer in reviewers:
            if convergence not in adjacency.get(reviewer, []):
                errors.append(f"{rel}: reviewer {reviewer!r} must transition to {convergence!r}")
        if final_gate not in reachable_from(convergence, adjacency):
            errors.append(f"{rel}: final gate is not reachable from review convergence")
        if set(adjacency.get(docs_node, [])) != set(reviewers):
            errors.append(f"{rel}: documentation node must dispatch directly to every required reviewer")
        for target in adjacency.get(convergence, []):
            if target == final_gate:
                continue
            if docs_node not in reachable_from(target, adjacency):
                errors.append(f"{rel}: remediation target {target!r} cannot return through documentation sync")
            if convergence in reachable_avoiding(target, adjacency, {docs_node}):
                errors.append(
                    f"{rel}: remediation target {target!r} can re-enter review convergence without documentation sync"
                )

    if entrypoint in nodes:
        reachable = reachable_from(entrypoint, adjacency)
        unreachable = set(nodes) - reachable
        if unreachable:
            errors.append(f"{rel}: unreachable nodes {sorted(unreachable)}")
        if not set(terminals).issubset(reachable):
            errors.append(f"{rel}: not every terminal is reachable from entrypoint")
        if docs_node in nodes:
            after_docs = reachable_from(docs_node, adjacency)
            missing_reviews = set(reviewers) - after_docs
            if missing_reviews:
                errors.append(f"{rel}: documentation node does not lead to reviews {sorted(missing_reviews)}")

    reverse: dict[str, list[str]] = {node_id: [] for node_id in nodes}
    for source, node_targets in adjacency.items():
        for target in node_targets:
            if target in reverse:
                reverse[target].append(source)
    if matching_convergence and final_gate in reverse:
        if set(reverse[final_gate]) != {matching_convergence[0]}:
            errors.append(f"{rel}: final_gate may be entered only from review convergence")
    can_finish: set[str] = set()
    pending = deque(terminals)
    while pending:
        current = pending.popleft()
        if current in can_finish:
            continue
        can_finish.add(current)
        pending.extend(reverse.get(current, []))
    dead_ends = set(nodes) - can_finish
    if dead_ends:
        errors.append(f"{rel}: nodes cannot reach a terminal {sorted(dead_ends)}")


def main() -> int:
    errors: list[str] = []
    schema_path = WORKFLOW_DIR / "workflow.schema.json"
    if load_json(schema_path, errors) is None:
        pass
    for workflow_id, path in EXPECTED_GRAPHS.items():
        if not path.exists():
            errors.append(f"{path.relative_to(ROOT)}: required workflow graph is missing")
            continue
        validate_graph(workflow_id, path, errors)

    if errors:
        print("Workflow graph check failed:")
        for error in errors:
            print(f"  - {error}")
        return 1
    print(f"Workflow graph check passed: {len(EXPECTED_GRAPHS)} graphs.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
