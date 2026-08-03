#!/usr/bin/env python3
"""Merge Go cover profiles (mode: atomic/count) by summing block counts.

Go has no built-in way to merge a `go test -coverprofile` text profile
with a `go tool covdata textfmt` one. Blocks are identical keys
(file:start,end numStmts); counts sum. `set` mode would need OR, so we
refuse it rather than merge wrong.
"""
import sys

def main() -> None:
    blocks: dict[str, int] = {}
    order: list[str] = []
    mode = None
    for path in sys.argv[1:]:
        with open(path) as f:
            for line in f:
                line = line.rstrip("\n")
                if not line:
                    continue
                if line.startswith("mode:"):
                    m = line.split(":", 1)[1].strip()
                    if m == "set":
                        sys.exit(f"{path}: refusing to merge 'set' mode profile")
                    if mode is None:
                        mode = m
                    continue
                key, count = line.rsplit(" ", 1)
                if key not in blocks:
                    blocks[key] = 0
                    order.append(key)
                blocks[key] += int(count)
    if mode is None:
        sys.exit("no profiles given")
    print(f"mode: {mode}")
    for key in order:
        print(f"{key} {blocks[key]}")

if __name__ == "__main__":
    main()
