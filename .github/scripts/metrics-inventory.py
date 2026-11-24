#!/usr/bin/env python3
"""Extract Prometheus metric definitions from the commercial-entitlements metrics package.

Outputs a JSON array of objects: [{name, type, help, labels}].
Used by metrics-registry-lint.sh and CI observability gates.

Usage: python3 .github/scripts/metrics-inventory.py
"""

import ast
import json
import os
import re
import sys

SERVICE_PREFIX = "commercial_entitlements_"
METRICS_DIR = "internal/adapter/outbound/metrics"

CONSTRUCTOR_MAP = {
    "NewCounterVec": "counter",
    "NewCounter": "counter",
    "NewGaugeVec": "gauge",
    "NewGauge": "gauge",
    "NewHistogramVec": "histogram",
    "NewHistogram": "histogram",
    "NewSummaryVec": "summary",
    "NewSummary": "summary",
}

metrics = []

def extract_string(node):
    """Extract string value from an AST node."""
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        return node.value
    return None

def parse_go_metrics(filepath):
    """Parse a Go source file for prometheus metric registrations using regex."""
    with open(filepath) as f:
        content = f.read()

    for constructor, metric_type in CONSTRUCTOR_MAP.items():
        pattern = rf'prometheus\.{constructor}\s*\(\s*prometheus\.\w+Opts\{{([^}}]+)\}}'
        for m in re.finditer(pattern, content, re.DOTALL):
            opts_text = m.group(1)
            name_m = re.search(r'Name:\s*"([^"]+)"', opts_text)
            help_m = re.search(r'Help:\s*"([^"]+)"', opts_text)
            name = name_m.group(1) if name_m else "unknown"
            help_text = help_m.group(1) if help_m else ""

            # Extract labels from second arg ([]string{"label1", ...})
            labels = re.findall(r'"([^"]+)"', opts_text.split("}", 1)[-1]) if "}" in opts_text else []

            metrics.append({
                "name": name,
                "type": metric_type,
                "help": help_text,
                "labels": labels,
                "file": filepath,
            })


if not os.path.isdir(METRICS_DIR):
    print("[]")
    sys.exit(0)

for fname in os.listdir(METRICS_DIR):
    if fname.endswith(".go") and not fname.endswith("_test.go"):
        try:
            parse_go_metrics(os.path.join(METRICS_DIR, fname))
        except Exception as e:
            print(f"# warning: could not parse {fname}: {e}", file=sys.stderr)

print(json.dumps(metrics, indent=2))
