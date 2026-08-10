#!/usr/bin/env python3
# Licensed under the Apache License, Version 2.0.
#
# postprocess-validate-tags.py
#
# openapi-generator's Go template only emits `validate:"regexp=..."` (from the
# @pattern trait). It silently drops @length constraints on strings and lists,
# and doesn't emit an integer range either. This post-processor reads the
# original OpenAPI spec (which still carries every constraint) and rewrites each
# struct field tag in the generated Go to include min/max derived from
# minLength/maxLength/minItems/maxItems/minimum/maximum.
#
# After this runs, gopkg.in/validator.v2 will enforce the full set at runtime
# when the SDK calls validator.Validate(&reg).
#
# Invocation:
#   python3 postprocess-validate-tags.py \
#       --spec build/smithy/source/openapi/HostServiceApi.openapi.json \
#       --dir generated/tr12go

import argparse
import json
import re
import sys
from pathlib import Path

FIELD_RE = re.compile(
    r'^(\s*)(\w+)\s+(\S+)\s+`json:"([^"]+)"(\s+validate:"([^"]*)")?`\s*$'
)


def extract_constraints(prop: dict) -> list[str]:
    """Return validate-tag fragments derived from an OpenAPI property schema."""
    parts = []
    if "pattern" in prop:
        # reflect.StructTag.Get() applies Go string-literal escaping to the
        # tag value. So a source-code tag "regexp=^\d+$" yields an empty
        # extracted value (\d is not a valid Go escape). We must emit "\\d"
        # in the source so Get() decodes it to "\d" for the regex engine.
        escaped = prop['pattern'].replace('\\', '\\\\')
        parts.append(f"regexp={escaped}")
    if "minLength" in prop:
        parts.append(f"min={prop['minLength']}")
    if "maxLength" in prop:
        parts.append(f"max={prop['maxLength']}")
    if "minItems" in prop:
        # validator.v2 uses `min` for slice length too
        parts.append(f"min={prop['minItems']}")
    if "maxItems" in prop:
        parts.append(f"max={prop['maxItems']}")
    if "minimum" in prop:
        parts.append(f"min={prop['minimum']}")
    if "maximum" in prop:
        parts.append(f"max={prop['maximum']}")
    return parts


def build_index(spec: dict) -> dict:
    """{ SchemaName: { jsonPropertyName: [constraint fragments] } }."""
    idx = {}
    schemas = spec.get("components", {}).get("schemas", {})
    for name, schema in schemas.items():
        if schema.get("type") != "object":
            continue
        props = schema.get("properties", {})
        for pname, pschema in props.items():
            constraints = extract_constraints(pschema)
            if constraints:
                idx.setdefault(name, {})[pname] = constraints
    return idx


TYPE_RE = re.compile(r"^type\s+(\w+)\s+struct\s*\{\s*$")


def rewrite_file(path: Path, idx: dict) -> int:
    """Rewrite validate tags in one generated file. Returns number of changes."""
    lines = path.read_text().splitlines(keepends=True)
    current_type = None
    changes = 0
    out = []
    for line in lines:
        m = TYPE_RE.match(line)
        if m:
            current_type = m.group(1)
            out.append(line)
            continue
        if current_type and current_type in idx:
            fm = FIELD_RE.match(line)
            if fm:
                indent, field, gotype, json_tag, _, existing_validate = fm.groups()
                # Strip the `,omitempty` suffix to find the raw JSON name
                json_name = json_tag.split(",")[0]
                if json_name in idx[current_type]:
                    wanted = idx[current_type][json_name]
                    # Overwrite the tag entirely with what the OpenAPI spec says.
                    # openapi-generator's default Go tag emitter over-escapes
                    # backslashes in patterns (e.g. ^\d+ becomes ^\\\\d+ in the
                    # struct tag, breaking runtime regex matching). Rebuilding
                    # the tag from the spec avoids that.
                    new_validate = ",".join(wanted)
                    new_line = (
                        f'{indent}{field} {gotype} `json:"{json_tag}" '
                        f'validate:"{new_validate}"`\n'
                    )
                    if new_line != line:
                        out.append(new_line)
                        changes += 1
                        continue
        # closing brace resets scope
        if line.strip() == "}":
            current_type = None
        out.append(line)
    if changes:
        path.write_text("".join(out))
    return changes


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--spec", required=True)
    ap.add_argument("--dir", required=True)
    args = ap.parse_args()

    spec = json.loads(Path(args.spec).read_text())
    idx = build_index(spec)

    total = 0
    files_touched = 0
    for gofile in sorted(Path(args.dir).glob("model_*.go")):
        n = rewrite_file(gofile, idx)
        if n:
            files_touched += 1
            total += n
            print(f"  {gofile.name}: {n} tag(s) enriched")
    print(f"OK: {total} tag(s) across {files_touched} file(s)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
