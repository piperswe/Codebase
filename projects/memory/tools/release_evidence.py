#!/usr/bin/env python3
"""Validate a Bazel OCI index and record immutable build identities."""
import argparse
import hashlib
import json
from pathlib import Path
import re


def record(layout, source, repository, workflow, checks):
    if not re.fullmatch(r"[0-9a-f]{40}", source):
        raise ValueError("source must be a full commit SHA")
    root = json.loads((layout / "index.json").read_text())
    if len(root["manifests"]) != 1:
        raise ValueError("expected one OCI index descriptor")
    digest = root["manifests"][0]["digest"]

    def blob(digest):
        if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
            raise ValueError("malformed digest")
        data = (layout / "blobs" / "sha256" / digest.split(":")[1]).read_bytes()
        if "sha256:" + hashlib.sha256(data).hexdigest() != digest:
            raise ValueError("OCI content does not match its digest")
        return json.loads(data)

    index = blob(digest)
    children = {}
    for child in index["manifests"]:
        platform = child["platform"]
        if platform["os"] != "linux" or platform["architecture"] not in ("amd64", "arm64"):
            raise ValueError("unexpected release platform")
        arch = platform["architecture"]
        if arch in children:
            raise ValueError("duplicate architecture")
        manifest = blob(child["digest"])
        config = blob(manifest["config"]["digest"])
        if config["config"].get("Labels", {}).get("org.opencontainers.image.revision") != source:
            raise ValueError("child image stamp does not match release source")
        if config["config"].get("Entrypoint") != ["/memoryd"]:
            raise ValueError("unexpected entrypoint")
        children[arch] = child["digest"]
    if set(children) != {"amd64", "arm64"}:
        raise ValueError("both architectures are required")
    return {"formatVersion": 1, "sourceCommit": source, "imageRepository": repository,
            "indexDigest": digest, "architectureDigests": children, "workflowRun": workflow,
            "schemaCompatibility": {"minimum": 1, "maximum": 1}, "validation": checks}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("layout", type=Path)
    p.add_argument("--source", required=True)
    p.add_argument("--repository", required=True)
    p.add_argument("--workflow-run", required=True)
    p.add_argument("--checks", type=Path, required=True, help="JSON evidence from completed CI jobs")
    p.add_argument("--output", type=Path, required=True)
    args = p.parse_args()
    checks = json.loads(args.checks.read_text())
    required = ("nativeAmd64", "nativeArm64", "goVulnerabilityScan", "imageVulnerabilityScan")
    if any(checks.get(check) != "passed" for check in required):
        raise ValueError("release evidence requires both native test and security gates")
    args.output.write_text(json.dumps(record(args.layout, args.source, args.repository, args.workflow_run, checks), indent=2) + "\n")


if __name__ == "__main__":
    main()
