"""Download QQP (GLUE) and write a two-phase cache replay file.

Usage: python bench/datasets/fetch_qqp.py --n 1000 --out bench/datasets/qqp-replay.jsonl
"""
import argparse
import json
import random
import sys

ap = argparse.ArgumentParser()
ap.add_argument("--n", type=int, default=1000, help="number of pairs (half duplicates, half not)")
ap.add_argument("--out", required=True)
ap.add_argument("--seed", type=int, default=42)
args = ap.parse_args()

try:
    from datasets import load_dataset
    ds = load_dataset("nyu-mll/glue", "qqp", split="validation")
    dups = [r for r in ds if r["label"] == 1]
    nons = [r for r in ds if r["label"] == 0]
except ImportError:
    # Direct download from HuggingFace dataset server if datasets library is not installed
    import urllib.request
    dups, nons = [], []
    target_half = args.n // 2
    offset = 0
    limit = 100
    while len(dups) < target_half or len(nons) < target_half:
        url = f"https://datasets-server.huggingface.co/rows?dataset=nyu-mll%2Fglue&config=qqp&split=validation&offset={offset}&limit={limit}"
        req = urllib.request.Request(url, headers={"User-Agent": "proofgate-bench"})
        with urllib.request.urlopen(req) as resp:
            data = json.loads(resp.read().decode("utf-8"))
            rows = data.get("rows", [])
            if not rows:
                break
            for row in rows:
                r = row["row"]
                if r["label"] == 1 and len(dups) < target_half:
                    dups.append(r)
                elif r["label"] == 0 and len(nons) < target_half:
                    nons.append(r)
            offset += len(rows)

rng = random.Random(args.seed)
pairs = [(r, True) for r in rng.sample(dups, min(len(dups), args.n // 2))] + \
        [(r, False) for r in rng.sample(nons, min(len(nons), args.n // 2))]
rng.shuffle(pairs)

with open(args.out, "w", encoding="utf-8") as f:
    for i, (r, dup) in enumerate(pairs):
        f.write(json.dumps({"id": f"p{i}-1", "group": f"p{i}", "phase": 1, "prompt": r["question1"]}) + "\n")
        f.write(json.dumps({"id": f"p{i}-2", "group": f"p{i}" if dup else f"n{i}", "phase": 2, "prompt": r["question2"]}) + "\n")
print(f"wrote {2 * len(pairs)} lines to {args.out}")
