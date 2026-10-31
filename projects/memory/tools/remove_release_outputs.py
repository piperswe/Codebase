#!/usr/bin/env python3
"""Remove only Memory's generated release outputs before a repeat-build check.

Bazel no-cache execution requirements prevent cache reuse when an action runs,
but do not invalidate an already-up-to-date output. Use a trusted Bazel execution
log and reject paths outside the active bazel-out tree. Source files are untouched.
"""
import json
import os
import stat
from pathlib import Path, PurePosixPath
import shutil
import sys


def outputs(log):
    text = Path(log).read_text()
    decoder = json.JSONDecoder()
    selected = set()
    while text.strip():
        text = text.lstrip()
        action, end = decoder.raw_decode(text)
        text = text[end:]
        if action.get("mnemonic") not in {"GoLink", "Tar", "OCIImage", "OCIIndex", "OCIDescriptor"}:
            continue
        for name in action.get("listedOutputs", []):
            p = PurePosixPath(name)
            if not p.parts or p.parts[0] != "bazel-out" or ".." in p.parts:
                continue
            if "/projects/memory/cmd/memoryd/" not in name:
                continue
            selected.add(name)
    if not selected:
        raise ValueError("no executed Memory release outputs found; repeat-build proof is unavailable")
    return sorted(selected, key=len, reverse=True)


def main():
    root = Path("bazel-out").resolve(strict=True)
    paths = outputs(sys.argv[1])
    # Validate all paths before changing any build artifact.
    for name in paths:
        Path(name).parent.resolve().relative_to(root)
    for name in paths:
        p = Path(name)
        if p.is_symlink() or p.is_file():
            p.unlink()
        elif p.is_dir():
            # Bazel marks generated tree artifacts read-only. Make only owned
            # output directories writable; never chmod a symlink target.
            for directory, dirs, _ in os.walk(p, followlinks=False):
                os.chmod(directory, os.stat(directory, follow_symlinks=False).st_mode | stat.S_IWUSR)
                dirs[:] = [d for d in dirs if not (Path(directory) / d).is_symlink()]
            shutil.rmtree(p)
    print(f"Removed {len(paths)} generated Memory outputs; source and dependency outputs preserved")


if __name__ == "__main__":
    main()
