#!/usr/bin/env python3
import json
import subprocess
import sys


def parse(path: str, output: list[str]) -> dict:
    result = {"file": path, "go": "", "path": "", "module": {}, "dependencies": {}, "build": {}, "replaces": []}
    for line in output:
        fields = line.strip().split("\t")
        if not fields:
            continue
        if ": go1." in fields[0]:
            result["go"] = fields[0].rsplit(": ", 1)[1]
        elif fields[0] == "path" and len(fields) >= 2:
            result["path"] = fields[1]
        elif fields[0] == "mod" and len(fields) >= 3:
            result["module"] = {"path": fields[1], "version": fields[2]}
        elif fields[0] == "dep" and len(fields) >= 3:
            result["dependencies"][fields[1]] = fields[2]
        elif fields[0] == "build" and len(fields) >= 2:
            name, _, value = fields[1].partition("=")
            result["build"][name] = value
        elif fields[0] == "=>":
            result["replaces"].append(fields[1:])
    return result


def read(path: str, from_file: bool) -> dict:
    if from_file:
        with open(path, encoding="utf-8") as handle:
            return parse(path, handle.read().splitlines())
    output = subprocess.run(
        ["go", "version", "-m", path], check=True, text=True, capture_output=True
    ).stdout.splitlines()
    return parse(path, output)


def main() -> None:
    from_file = len(sys.argv) > 1 and sys.argv[1] == "--from-files"
    paths = sys.argv[2:] if from_file else sys.argv[1:]
    if len(paths) < 2:
        raise SystemExit("usage: compare_buildinfo.py [--from-files] <host> <plugin> [plugin ...]")
    host = read(paths[0], from_file)
    plugins = []
    for path in paths[1:]:
        plugin = read(path, from_file)
        common = sorted(set(host["dependencies"]) & set(plugin["dependencies"]))
        plugin["comparison"] = {
            "go_matches": host["go"] == plugin["go"],
            "common_modules": len(common),
            "module_mismatches": [
                {
                    "module": name,
                    "host": host["dependencies"][name],
                    "plugin": plugin["dependencies"][name],
                }
                for name in common
                if host["dependencies"][name] != plugin["dependencies"][name]
            ],
            "shared_build_settings": {
                name: {"host": host["build"][name], "plugin": plugin["build"][name]}
                for name in sorted(set(host["build"]) & set(plugin["build"]))
            },
        }
        plugins.append(plugin)
    print(json.dumps({"host": host, "plugins": plugins}, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
