"""Download QQP (GLUE) and write a two-phase cache replay file.

Usage: uvx --with datasets python bench/datasets/fetch_qqp.py --n 1000 --out bench/datasets/qqp-replay.jsonl
"""
import argparse
import json
import random

from datasets import load_dataset

ap = argparse.ArgumentParser()
ap.add_argument("--n", type=int, default=1000, help="number of pairs (half duplicates, half not)")
ap.add_argument("--out", required=True)
ap.add_argument("--seed", type=int, default=42)
args = ap.parse_args()

ds = load_dataset("nyu-mll/glue", "qqp", split="validation")
rng = random.Random(args.seed)
dups = [r for r in ds if r["label"] == 1]
nons = [r for r in ds if r["label"] == 0]
pairs = [(r, True) for r in rng.sample(dups, args.n // 2)] + [(r, False) for r in rng.sample(nons, args.n // 2)]
rng.shuffle(pairs)

with open(args.out, "w", encoding="utf-8") as f:
    for i, (r, dup) in enumerate(pairs):
        f.write(json.dumps({"id": f"p{i}-1", "group": f"p{i}", "phase": 1, "prompt": r["question1"]}) + "\n")
        f.write(json.dumps({"id": f"p{i}-2", "group": f"p{i}" if dup else f"n{i}", "phase": 2, "prompt": r["question2"]}) + "\n")
print(f"wrote {2 * len(pairs)} lines to {args.out}")
