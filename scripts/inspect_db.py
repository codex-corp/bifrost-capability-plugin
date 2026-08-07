#!/usr/bin/env python3
import json
import sqlite3
import sys


def die(message: str) -> None:
    raise SystemExit(message)


def configured_models(db_path: str) -> set[str]:
    connection = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    try:
        rows = connection.execute(
            "SELECT provider, models_json FROM config_keys WHERE enabled = 1"
        )
        result: set[str] = set()
        for provider, raw_models in rows:
            for model in json.loads(raw_models or "[]"):
                result.add(f"{provider}/{model}")
        return result
    finally:
        connection.close()


def validate_models(db_path: str, model_path: str) -> None:
    with open(model_path, encoding="utf-8") as handle:
        required = set(json.load(handle)["models"].values())
    missing = sorted(required - configured_models(db_path))
    if missing:
        die("Models missing from enabled provider keys: " + ", ".join(missing))
    print(f"Verified {len(required)} configured model identifiers.")


if len(sys.argv) == 4 and sys.argv[1] == "validate-models":
    validate_models(sys.argv[2], sys.argv[3])
else:
    die("usage: inspect_db.py validate-models <config.db> <models.json>")
