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


def validate_virtual_key(db_path: str, model_path: str, virtual_key_id: str) -> None:
    with open(model_path, encoding="utf-8") as handle:
        required = {value.split("/", 1)[1] for value in json.load(handle)["models"].values()}
    connection = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    try:
        row = connection.execute(
            "SELECT name, is_active FROM governance_virtual_keys WHERE id = ?", (virtual_key_id,)
        ).fetchone()
        if not row:
            die(f"Virtual Key not found: {virtual_key_id}")
        if not row[1]:
            die(f"Virtual Key is disabled: {row[0]}")
        provider = connection.execute(
            "SELECT allowed_models FROM governance_virtual_key_provider_configs "
            "WHERE virtual_key_id = ? AND lower(provider) = 'bedrock'",
            (virtual_key_id,),
        ).fetchone()
        if not provider:
            die(f"Virtual Key does not permit the Bedrock provider: {row[0]}")
        allowed = set(json.loads(provider[0] or "[]"))
        missing = sorted(required - allowed) if "*" not in allowed else []
        if missing:
            die("Virtual Key does not permit models: " + ", ".join(missing))
        print(f"Verified Bedrock access for Virtual Key: {row[0]} ({virtual_key_id})")
    finally:
        connection.close()


def list_virtual_keys(db_path: str) -> None:
    connection = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    try:
        rows = connection.execute(
            "SELECT id, name, is_active FROM governance_virtual_keys ORDER BY name, id"
        )
        for virtual_key_id, name, active in rows:
            print(f"{virtual_key_id}\t{name}\t{'active' if active else 'disabled'}")
    finally:
        connection.close()


if len(sys.argv) == 4 and sys.argv[1] == "validate-models":
    validate_models(sys.argv[2], sys.argv[3])
elif len(sys.argv) == 5 and sys.argv[1] == "validate-virtual-key":
    validate_virtual_key(sys.argv[2], sys.argv[3], sys.argv[4])
elif len(sys.argv) == 3 and sys.argv[1] == "list-virtual-keys":
    list_virtual_keys(sys.argv[2])
else:
    die("usage: inspect_db.py validate-models <config.db> <models.json> | validate-virtual-key <config.db> <models.json> <id> | list-virtual-keys <config.db>")
